package image

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

var ErrFileNotFound = errors.New("file not found in image")
var ErrPathNotFound = errors.New("path not found in image")
var ErrEmptyImage = errors.New("image filesystem is empty")

func pathHiddenByWhiteout(target, whiteout string) bool {
	target = strings.Trim(strings.TrimPrefix(target, "/"), "/")
	whiteout = strings.Trim(strings.TrimPrefix(whiteout, "/"), "/")
	return target == whiteout || strings.HasPrefix(target, whiteout+"/")
}

// fileExistsInImage checks whether a path exists in any layer of the image.
func fileExistsInImage(img v1.Image, targetPath string) (bool, error) {
	layers, err := img.Layers()
	if err != nil {
		return false, err
	}

	targetPath = strings.TrimPrefix(targetPath, "/")
	deletedPrefixes := map[string]struct{}{}

	for i := len(layers) - 1; i >= 0; i-- {
		rc, err := layers[i].Uncompressed()
		if err != nil {
			return false, err
		}

		tr := tar.NewReader(rc)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				rc.Close()
				return false, err
			}

			name := strings.TrimPrefix(hdr.Name, "./")
			name = strings.TrimPrefix(name, "/")

			hidden := false
			for deleted := range deletedPrefixes {
				if pathHiddenByWhiteout(name, deleted) {
					hidden = true
					break
				}
			}
			if hidden {
				continue
			}

			if name == targetPath {
				rc.Close()
				return true, nil
			}

			// whiteout means deleted
			dir := path.Dir(targetPath)
			base := path.Base(targetPath)
			if name == path.Join(dir, ".wh."+base) {
				rc.Close()
				return false, nil
			}
			if strings.HasPrefix(path.Base(name), ".wh.") {
				realName := path.Join(path.Dir(name), strings.TrimPrefix(path.Base(name), ".wh."))
				deletedPrefixes[strings.TrimPrefix(realName, "/")] = struct{}{}
				if pathHiddenByWhiteout(targetPath, realName) {
					rc.Close()
					return false, nil
				}
			}
		}
		rc.Close()
	}

	return false, nil
}

// fileExistsInDirs checks whether a file with the given basename exists
// in any of the specified directories across all image layers.
func fileExistsInDirs(img v1.Image, basename string, dirs []string) (bool, error) {
	dirSet := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		dirSet[strings.TrimSuffix(strings.TrimPrefix(d, "/"), "/")] = true
	}

	layers, err := img.Layers()
	if err != nil {
		return false, err
	}

	// Track whiteouts per layer so we respect deletions.
	type entry struct{ exists bool }
	seen := make(map[string]entry)
	deletedPrefixes := make(map[string]struct{})

	for i := len(layers) - 1; i >= 0; i-- {
		rc, err := layers[i].Uncompressed()
		if err != nil {
			return false, err
		}
		tr := tar.NewReader(rc)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				rc.Close()
				return false, err
			}

			name := strings.TrimPrefix(hdr.Name, "./")
			name = strings.TrimPrefix(name, "/")
			dir := strings.TrimSuffix(path.Dir(name), "/")
			base := path.Base(name)

			// whiteout entry
			if strings.HasPrefix(base, ".wh.") {
				realBase := strings.TrimPrefix(base, ".wh.")
				key := dir + "/" + realBase
				if _, already := seen[key]; !already {
					seen[key] = entry{exists: false}
				}
				deletedPrefixes[key] = struct{}{}
				continue
			}

			hidden := false
			for deleted := range deletedPrefixes {
				if pathHiddenByWhiteout(name, deleted) {
					hidden = true
					break
				}
			}
			if hidden {
				continue
			}

			if base == basename && dirSet[dir] {
				key := dir + "/" + base
				if _, already := seen[key]; !already {
					seen[key] = entry{exists: true}
				}
			}
		}
		rc.Close()
	}

	for _, e := range seen {
		if e.exists {
			return true, nil
		}
	}
	return false, nil
}

// findAllByBasename returns all active paths in the image that have the given basename.
// It respects whiteout entries — deleted files are not returned.
func findAllByBasename(img v1.Image, basename string) ([]string, error) {
	layers, err := img.Layers()
	if err != nil {
		return nil, err
	}

	// Process layers newest-first; first time we see a path wins.
	type entry struct {
		path   string
		exists bool
	}
	seen := make(map[string]entry)
	deletedPrefixes := make(map[string]struct{})

	for i := len(layers) - 1; i >= 0; i-- {
		rc, err := layers[i].Uncompressed()
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(rc)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				rc.Close()
				return nil, err
			}

			name := strings.TrimPrefix(hdr.Name, "./")
			name = strings.TrimPrefix(name, "/")
			dir := path.Dir(name)
			base := path.Base(name)

			if strings.HasPrefix(base, ".wh.") {
				realBase := strings.TrimPrefix(base, ".wh.")
				key := path.Join(dir, realBase)
				if _, already := seen[key]; !already {
					seen[key] = entry{path: key, exists: false}
				}
				deletedPrefixes[key] = struct{}{}
				continue
			}

			hidden := false
			for deleted := range deletedPrefixes {
				if pathHiddenByWhiteout(name, deleted) {
					hidden = true
					break
				}
			}
			if hidden {
				continue
			}

			if base == basename {
				if _, already := seen[name]; !already {
					seen[name] = entry{path: name, exists: true}
				}
			}
		}
		rc.Close()
	}

	var result []string
	for _, e := range seen {
		if e.exists {
			result = append(result, "/"+e.path)
		}
	}
	return result, nil
}

// AddFileLayerConditionalInDirs adds a file only if basename is absent in all given dirs.
func AddFileLayerConditionalInDirs(img v1.Image, localSrc, dstPath, mode, owner string, dirs []string) (v1.Image, error) {
	resolvedSrc, resolvedDst, err := resolveAddFilePaths(localSrc, dstPath)
	if err != nil {
		return nil, err
	}

	exists, err := fileExistsInDirs(img, path.Base(resolvedDst), dirs)
	if err != nil {
		return nil, fmt.Errorf("checking existence in dirs: %w", err)
	}
	if exists {
		return img, nil
	}
	return addResolvedFileLayer(img, resolvedSrc, resolvedDst, mode, owner)
}

// ReplaceFile removes every occurrence of findBasename from the image (whiteout),
// then adds localSrc at dstPath. If no occurrences are found, it simply adds the file.
func ReplaceFile(img v1.Image, localSrc, dstPath, mode, owner, findBasename string) (v1.Image, error) {
	if findBasename != "" {
		paths, err := findAllByBasename(img, findBasename)
		if err != nil {
			return nil, fmt.Errorf("searching for %q: %w", findBasename, err)
		}
		for _, p := range paths {
			img, err = RemoveFile(img, p)
			if err != nil {
				return nil, fmt.Errorf("removing %q: %w", p, err)
			}
		}
	}
	return AddFileLayer(img, localSrc, dstPath, mode, owner)
}

// AddFileLayer creates a new image layer containing a single local file.
func AddFileLayer(img v1.Image, localSrc, dstPath, mode, owner string) (v1.Image, error) {
	resolvedSrc, resolvedDst, err := resolveAddFilePaths(localSrc, dstPath)
	if err != nil {
		return nil, err
	}
	return addResolvedFileLayer(img, resolvedSrc, resolvedDst, mode, owner)
}

func addResolvedFileLayer(img v1.Image, localSrc, dstPath, mode, owner string) (v1.Image, error) {
	data, err := os.ReadFile(localSrc)
	if err != nil {
		return nil, fmt.Errorf("reading %q: %w", localSrc, err)
	}

	perm, err := parseMode(mode, 0o644)
	if err != nil {
		return nil, err
	}

	uid, gid := parseOwner(owner)

	layer, err := singleFileTarLayer(dstPath, data, perm, uid, gid)
	if err != nil {
		return nil, err
	}

	return mutate.AppendLayers(img, layer)
}

func resolveAddFilePaths(localSrc, dstPath string) (string, string, error) {
	resolvedSrc := localSrc
	if hasGlobMeta(localSrc) {
		matches, err := filepath.Glob(localSrc)
		if err != nil {
			return "", "", fmt.Errorf("invalid src glob %q: %w", localSrc, err)
		}
		sort.Strings(matches)
		switch len(matches) {
		case 0:
			return "", "", fmt.Errorf("src glob %q matched no files", localSrc)
		case 1:
			resolvedSrc = matches[0]
		default:
			return "", "", fmt.Errorf("src glob %q matched multiple files: %s", localSrc, strings.Join(matches, ", "))
		}
	}

	if strings.HasSuffix(dstPath, "/") {
		dstPath = path.Join(dstPath, filepath.Base(resolvedSrc))
	}
	return resolvedSrc, dstPath, nil
}

func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// AddDirLayer appends all files from a local directory as a new layer.
func AddDirLayer(img v1.Image, localSrc, dstPath, mode, owner string) (v1.Image, error) {
	perm, err := parseMode(mode, 0o644)
	if err != nil {
		return nil, err
	}
	uid, gid := parseOwner(owner)

	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)

	entries, err := os.ReadDir(localSrc)
	if err != nil {
		return nil, fmt.Errorf("reading dir %q: %w", localSrc, err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(localSrc + "/" + e.Name())
		if err != nil {
			return nil, err
		}
		if err := writeTarEntry(tw, path.Join(dstPath, e.Name()), data, perm, uid, gid); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	layer, err := tarball.LayerFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		return nil, err
	}

	return mutate.AppendLayers(img, layer)
}

// AddFileLayerConditional adds a file only when condition allows it.
func AddFileLayerConditional(img v1.Image, localSrc, dstPath, mode, owner, ifNotExists string) (v1.Image, error) {
	resolvedSrc, resolvedDst, err := resolveAddFilePaths(localSrc, dstPath)
	if err != nil {
		return nil, err
	}
	if ifNotExists != "" {
		exists, err := fileExistsInImage(img, ifNotExists)
		if err != nil {
			return nil, fmt.Errorf("checking existence of %q: %w", ifNotExists, err)
		}
		if exists {
			return img, nil
		}
	}
	return addResolvedFileLayer(img, resolvedSrc, resolvedDst, mode, owner)
}

// RemoveFile appends a whiteout entry that removes a path from the image.
func RemoveFile(img v1.Image, dstPath string) (v1.Image, error) {
	dir := path.Dir(dstPath)
	base := path.Base(dstPath)
	whiteout := path.Join(dir, ".wh."+base)

	layer, err := singleFileTarLayer(whiteout, nil, 0o644, 0, 0)
	if err != nil {
		return nil, err
	}

	return mutate.AppendLayers(img, layer)
}

// CopyFileFrom extracts a file from srcImage and adds it to dstImage as a new layer.
func CopyFileFrom(dstImg v1.Image, srcImg v1.Image, srcPath, dstPath, mode, owner string) (v1.Image, error) {
	data, err := extractFileFromImage(srcImg, srcPath)
	if err != nil {
		return nil, fmt.Errorf("extracting %q from source image: %w", srcPath, err)
	}

	perm, err := parseMode(mode, 0o644)
	if err != nil {
		return nil, err
	}
	uid, gid := parseOwner(owner)

	layer, err := singleFileTarLayer(dstPath, data, perm, uid, gid)
	if err != nil {
		return nil, err
	}

	return mutate.AppendLayers(dstImg, layer)
}

// ExtractFileToLocal extracts a file from an image to a local path.
func ExtractFileToLocal(img v1.Image, srcPath, localDst string) error {
	data, err := extractFileFromImage(img, srcPath)
	if err != nil {
		return err
	}
	return os.WriteFile(localDst, data, 0o644)
}

// ReadFile returns a regular file from the effective image filesystem.
func ReadFile(img v1.Image, srcPath string) ([]byte, error) {
	normalizedPath := normalizeImagePath(srcPath)
	if normalizedPath == "/" {
		return nil, fmt.Errorf("path %q is not a regular file", normalizedPath)
	}

	data, err := extractFileFromImage(img, normalizedPath)
	if err != nil {
		if errors.Is(err, ErrFileNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrFileNotFound, normalizedPath)
		}
		return nil, err
	}
	return data, nil
}

// UnzipTo extracts a zip/jar file from inside the image and adds the contents as a new layer.
func UnzipTo(img v1.Image, srcPath, dstDir, owner string) (v1.Image, error) {
	data, err := extractFileFromImage(img, srcPath)
	if err != nil {
		return nil, fmt.Errorf("extracting zip %q: %w", srcPath, err)
	}

	uid, gid := parseOwner(owner)

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("opening zip: %w", err)
	}

	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}

		dstPath := path.Join(dstDir, f.Name)
		if err := writeTarEntry(tw, dstPath, content, f.Mode(), uid, gid); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	layer, err := tarball.LayerFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		return nil, err
	}

	return mutate.AppendLayers(img, layer)
}

// --- helpers ---

func extractFileFromImage(img v1.Image, targetPath string) ([]byte, error) {
	layers, err := img.Layers()
	if err != nil {
		return nil, err
	}

	targetPath = strings.TrimPrefix(targetPath, "/")

	for i := len(layers) - 1; i >= 0; i-- {
		rc, err := layers[i].Uncompressed()
		if err != nil {
			return nil, err
		}

		tr := tar.NewReader(rc)
		var found []byte
		layerWhiteouts := map[string]struct{}{}
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				rc.Close()
				return nil, err
			}

			name := strings.TrimPrefix(hdr.Name, "./")
			name = strings.TrimPrefix(name, "/")
			name = strings.TrimSuffix(name, "/")
			if strings.HasPrefix(path.Base(name), ".wh.") {
				realName := path.Join(path.Dir(name), strings.TrimPrefix(path.Base(name), ".wh."))
				layerWhiteouts[strings.TrimPrefix(realName, "/")] = struct{}{}
				continue
			}

			if name == targetPath {
				if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA && hdr.Typeflag != 0 {
					rc.Close()
					return nil, fmt.Errorf("path %q is not a regular file", targetPath)
				}
				data, err := io.ReadAll(tr)
				if err != nil {
					rc.Close()
					return nil, err
				}
				found = data
			}
		}
		rc.Close()
		if found != nil {
			return found, nil
		}
		for deleted := range layerWhiteouts {
			if pathHiddenByWhiteout(targetPath, deleted) {
				return nil, fmt.Errorf("%w: %s (whiteout)", ErrFileNotFound, targetPath)
			}
		}
	}

	return nil, fmt.Errorf("%w: %s", ErrFileNotFound, targetPath)
}

func singleFileTarLayer(dstPath string, data []byte, perm os.FileMode, uid, gid int) (v1.Layer, error) {
	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)

	if err := writeTarEntry(tw, dstPath, data, perm, uid, gid); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return tarball.LayerFromReader(bytes.NewReader(buf.Bytes()))
}

func writeTarEntry(tw *tar.Writer, dstPath string, data []byte, perm os.FileMode, uid, gid int) error {
	return writeTarHeaderEntry(tw, dstPath, data, perm, uid, gid, tar.TypeReg, "")
}

func writeTarDirEntry(tw *tar.Writer, dstPath string, perm os.FileMode, uid, gid int) error {
	return writeTarHeaderEntry(tw, dstPath, nil, perm, uid, gid, tar.TypeDir, "")
}

func writeTarHeaderEntry(tw *tar.Writer, dstPath string, data []byte, perm os.FileMode, uid, gid int, typeflag byte, linkname string) error {
	dstPath = strings.TrimPrefix(dstPath, "/")
	dstPath = strings.TrimSuffix(dstPath, "/")
	if dstPath == "" {
		return nil
	}

	size := int64(len(data))
	switch typeflag {
	case tar.TypeDir, tar.TypeSymlink, tar.TypeLink:
		size = 0
	}

	hdr := &tar.Header{
		Name:     dstPath,
		Size:     size,
		Mode:     tarHeaderMode(perm),
		Uid:      uid,
		Gid:      gid,
		ModTime:  time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		Typeflag: typeflag,
		Linkname: linkname,
	}

	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if size == 0 {
		return nil
	}
	_, err := tw.Write(data)
	return err
}

type TreeEntry struct {
	Path     string
	Data     []byte
	Mode     os.FileMode
	UID      int
	GID      int
	ModTime  time.Time
	IsDir    bool
	Typeflag byte
	Linkname string
}

func normalizeImagePath(p string) string {
	return "/" + strings.Trim(strings.TrimPrefix(p, "/"), "/")
}

// CollectTree returns the effective file tree rooted at targetPath.
func CollectTree(img v1.Image, targetPath string) ([]TreeEntry, error) {
	layers, err := img.Layers()
	if err != nil {
		return nil, err
	}

	targetPath = strings.Trim(strings.TrimPrefix(targetPath, "/"), "/")
	includeAll := targetPath == ""

	type collected struct {
		TreeEntry
	}

	seen := make(map[string]collected)
	blockedPrefixes := map[string]struct{}{}

	isBlocked := func(name string) bool {
		for prefix := range blockedPrefixes {
			if pathHiddenByWhiteout(name, prefix) {
				return true
			}
		}
		return false
	}

	for i := len(layers) - 1; i >= 0; i-- {
		rc, err := layers[i].Uncompressed()
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(rc)
		layerEntries := make(map[string]collected)
		layerWhiteouts := map[string]struct{}{}
		layerOpaque := map[string]struct{}{}
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				rc.Close()
				return nil, err
			}

			name := strings.TrimPrefix(strings.TrimPrefix(hdr.Name, "./"), "/")
			name = strings.TrimSuffix(name, "/")
			if name == "" {
				continue
			}

			dir := path.Dir(name)
			base := path.Base(name)

			if base == ".wh..wh..opq" {
				prefix := strings.Trim(strings.TrimPrefix(dir, "/"), "/")
				layerOpaque[prefix] = struct{}{}
				continue
			}

			if strings.HasPrefix(base, ".wh.") {
				realName := path.Join(dir, strings.TrimPrefix(base, ".wh."))
				realName = strings.Trim(strings.TrimPrefix(realName, "/"), "/")
				layerWhiteouts[realName] = struct{}{}
				continue
			}

			if isBlocked(name) {
				continue
			}

			data, err := io.ReadAll(tr)
			if err != nil {
				rc.Close()
				return nil, err
			}

			layerEntries[name] = collected{
				TreeEntry: TreeEntry{
					Path:     "/" + name,
					Data:     data,
					Mode:     hdr.FileInfo().Mode(),
					UID:      hdr.Uid,
					GID:      hdr.Gid,
					ModTime:  hdr.ModTime,
					IsDir:    hdr.Typeflag == tar.TypeDir,
					Typeflag: hdr.Typeflag,
					Linkname: hdr.Linkname,
				},
			}
		}
		rc.Close()

		for name, entry := range layerEntries {
			if _, already := seen[name]; already {
				continue
			}
			seen[name] = entry
		}
		for prefix := range layerOpaque {
			blockedPrefixes[prefix] = struct{}{}
		}
		for prefix := range layerWhiteouts {
			blockedPrefixes[prefix] = struct{}{}
		}
	}

	entries := map[string]TreeEntry{}
	for name, entry := range seen {
		if !includeAll && !(name == targetPath || strings.HasPrefix(name, targetPath+"/")) {
			continue
		}
		entries["/"+name] = entry.TreeEntry
	}
	if len(entries) == 0 {
		if includeAll {
			return nil, ErrEmptyImage
		}
		return nil, fmt.Errorf("%w: %s", ErrPathNotFound, "/"+targetPath)
	}

	for absPath, entry := range entries {
		if includeAll {
			continue
		}
		cur := strings.Trim(strings.TrimPrefix(absPath, "/"), "/")
		for {
			parent := path.Dir(cur)
			if parent == "." || parent == "/" || parent == cur {
				break
			}
			if parent == targetPath || strings.HasPrefix(cur, targetPath+"/") {
				key := "/" + parent
				if _, ok := entries[key]; !ok {
					entries[key] = TreeEntry{
						Path:     key,
						Mode:     0o755,
						UID:      entry.UID,
						GID:      entry.GID,
						IsDir:    true,
						Typeflag: tar.TypeDir,
					}
				}
			}
			cur = parent
		}
	}

	var result []TreeEntry
	for _, entry := range entries {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path == result[j].Path {
			return result[i].IsDir && !result[j].IsDir
		}
		if strings.Count(result[i].Path, "/") != strings.Count(result[j].Path, "/") {
			return strings.Count(result[i].Path, "/") < strings.Count(result[j].Path, "/")
		}
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return result[i].Path < result[j].Path
	})
	return result, nil
}

// ListEntries returns the immediate entries under targetPath, or the file itself.
func ListEntries(img v1.Image, targetPath string) ([]TreeEntry, error) {
	targetPath = normalizeImagePath(targetPath)
	entries, err := CollectTree(img, targetPath)
	if err != nil {
		return nil, err
	}

	if targetPath != "/" {
		for _, entry := range entries {
			if entry.Path == targetPath && !entry.IsDir {
				return []TreeEntry{entry}, nil
			}
		}
	}

	entryMap := make(map[string]TreeEntry, len(entries))
	for _, entry := range entries {
		entryMap[entry.Path] = entry
	}

	childPaths := map[string]struct{}{}
	for _, entry := range entries {
		if entry.Path == targetPath {
			continue
		}
		if targetPath != "/" && !strings.HasPrefix(entry.Path, targetPath+"/") {
			continue
		}

		rel := strings.TrimPrefix(entry.Path, targetPath)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			continue
		}

		child := rel
		if i := strings.IndexByte(rel, '/'); i >= 0 {
			child = rel[:i]
		}
		childPaths[path.Join(targetPath, child)] = struct{}{}
	}

	result := make([]TreeEntry, 0, len(childPaths))
	for childPath := range childPaths {
		if entry, ok := entryMap[childPath]; ok {
			result = append(result, entry)
			continue
		}
		result = append(result, TreeEntry{
			Path:     childPath,
			Mode:     0o755,
			IsDir:    true,
			Typeflag: tar.TypeDir,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		left := strings.ToLower(path.Base(result[i].Path))
		right := strings.ToLower(path.Base(result[j].Path))
		if left != right {
			return left < right
		}
		return result[i].Path < result[j].Path
	})
	return result, nil
}

// FixPermissions rebuilds a subtree with updated ownership and/or modes.
func FixPermissions(img v1.Image, targetPath, owner, mode string, setgidDirs bool) (v1.Image, error) {
	entries, err := CollectTree(img, targetPath)
	if err != nil {
		return nil, err
	}

	targetPath = strings.TrimSpace(targetPath)
	targetPath = "/" + strings.Trim(strings.TrimPrefix(targetPath, "/"), "/")
	rootDir := path.Dir(targetPath)
	rootBase := path.Base(targetPath)
	whiteout := path.Join(rootDir, ".wh."+rootBase)

	overrideOwner := owner != ""
	newUID, newGID := parseOwner(owner)

	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)

	if err := writeTarEntry(tw, whiteout, nil, 0o644, 0, 0); err != nil {
		return nil, err
	}

	for _, entry := range entries {
		entryMode, err := applyPermissionMode(entry.Mode, mode, entry.IsDir, setgidDirs)
		if err != nil {
			return nil, err
		}
		uid, gid := entry.UID, entry.GID
		if overrideOwner {
			uid, gid = newUID, newGID
		}

		if entry.IsDir {
			if err := writeTarHeaderEntry(tw, entry.Path, nil, entryMode, uid, gid, tar.TypeDir, ""); err != nil {
				return nil, err
			}
			continue
		}
		if err := writeTarHeaderEntry(tw, entry.Path, entry.Data, entryMode, uid, gid, entry.Typeflag, entry.Linkname); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	layer, err := tarball.LayerFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		return nil, err
	}
	return mutate.AppendLayers(img, layer)
}

func applyPermissionMode(current os.FileMode, spec string, isDir, setgidDirs bool) (os.FileMode, error) {
	mode := current
	switch spec {
	case "":
	case "g=u":
		perm := current.Perm()
		userBits := (perm >> 6) & 0o7
		perm &^= 0o070
		perm |= userBits << 3
		mode = (current &^ os.ModePerm) | perm
	default:
		perm, err := parseMode(spec, current.Perm())
		if err != nil {
			return 0, err
		}
		mode = (current &^ os.ModePerm) | perm
	}
	if isDir && setgidDirs {
		mode |= os.ModeSetgid
	}
	return mode, nil
}

func tarHeaderMode(mode os.FileMode) int64 {
	m := int64(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		m |= 0o4000
	}
	if mode&os.ModeSetgid != 0 {
		m |= 0o2000
	}
	if mode&os.ModeSticky != 0 {
		m |= 0o1000
	}
	return m
}

func parseMode(s string, def os.FileMode) (os.FileMode, error) {
	if s == "" {
		return def, nil
	}
	var m uint32
	_, err := fmt.Sscanf(s, "%o", &m)
	if err != nil {
		return 0, fmt.Errorf("invalid mode %q: %w", s, err)
	}
	return os.FileMode(m), nil
}

func parseOwner(s string) (uid, gid int) {
	if s == "" {
		return 0, 0
	}
	fmt.Sscanf(s, "%d:%d", &uid, &gid)
	return uid, gid
}
