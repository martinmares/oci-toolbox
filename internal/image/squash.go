package image

import (
	"archive/tar"
	"bytes"
	"path"
	"sort"
	"strings"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// Squash materializes the effective final filesystem into a single layer.
// Deleted or shadowed paths are omitted from the result.
func Squash(img v1.Image) (v1.Image, error) {
	entries, err := CollectTree(img, "/")
	if err != nil {
		return nil, err
	}
	sortTarEntries(entries)

	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	for _, entry := range entries {
		if err := writeTarHeaderEntry(tw, entry.Path, entry.Data, entry.Mode, entry.UID, entry.GID, entry.Typeflag, entry.Linkname); err != nil {
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

	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	cfg = cfg.DeepCopy()
	cfg.History = nil
	cfg.RootFS.DiffIDs = nil

	base, err := mutate.ConfigFile(empty.Image, cfg)
	if err != nil {
		return nil, err
	}

	return mutate.Append(base, mutate.Addendum{Layer: layer})
}

// ReduceToBase preserves the base image layers and materializes only the
// effective delta from base to final as a single additional layer.
func ReduceToBase(baseImg, finalImg v1.Image) (v1.Image, error) {
	baseEntries, err := CollectTree(baseImg, "/")
	if err != nil {
		return nil, err
	}
	finalEntries, err := CollectTree(finalImg, "/")
	if err != nil {
		return nil, err
	}

	baseMap := treeEntryMap(baseEntries)
	finalMap := treeEntryMap(finalEntries)

	var whiteouts []string
	var additions []TreeEntry

	for p, be := range baseMap {
		fe, ok := finalMap[p]
		if !ok {
			whiteouts = append(whiteouts, p)
			continue
		}
		if treeEntryTypeChanged(be, fe) {
			whiteouts = append(whiteouts, p)
		}
		if !treeEntryEqual(be, fe) {
			additions = append(additions, fe)
		}
	}
	for p, fe := range finalMap {
		if _, ok := baseMap[p]; !ok {
			additions = append(additions, fe)
		}
	}

	whiteouts = compactWhiteouts(whiteouts)
	sortTarEntries(additions)

	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	for _, p := range whiteouts {
		whiteout := path.Join(path.Dir(p), ".wh."+path.Base(p))
		if err := writeTarEntry(tw, whiteout, nil, 0o644, 0, 0); err != nil {
			return nil, err
		}
	}
	for _, entry := range additions {
		if err := writeTarHeaderEntry(tw, entry.Path, entry.Data, entry.Mode, entry.UID, entry.GID, entry.Typeflag, entry.Linkname); err != nil {
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

	cfg, err := finalImg.ConfigFile()
	if err != nil {
		return nil, err
	}
	cfg = cfg.DeepCopy()

	img, err := mutate.Append(baseImg, mutate.Addendum{Layer: layer})
	if err != nil {
		return nil, err
	}
	currentCfg, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	cfg.RootFS = currentCfg.RootFS
	cfg.History = currentCfg.History
	return mutate.ConfigFile(img, cfg)
}

func treeEntryMap(entries []TreeEntry) map[string]TreeEntry {
	m := make(map[string]TreeEntry, len(entries))
	for _, e := range entries {
		m[e.Path] = e
	}
	return m
}

func treeEntryEqual(a, b TreeEntry) bool {
	if a.Typeflag != b.Typeflag || a.Linkname != b.Linkname || a.UID != b.UID || a.GID != b.GID || a.Mode != b.Mode || !a.ModTime.Equal(b.ModTime) {
		return false
	}
	if a.IsDir || a.Typeflag == tar.TypeDir || a.Typeflag == tar.TypeSymlink || a.Typeflag == tar.TypeLink {
		return true
	}
	return bytes.Equal(a.Data, b.Data)
}

func treeEntryTypeChanged(a, b TreeEntry) bool {
	return a.Typeflag != b.Typeflag || a.IsDir != b.IsDir
}

func compactWhiteouts(paths []string) []string {
	sort.Slice(paths, func(i, j int) bool {
		di := strings.Count(paths[i], "/")
		dj := strings.Count(paths[j], "/")
		if di != dj {
			return di < dj
		}
		return paths[i] < paths[j]
	})
	var out []string
	for _, p := range paths {
		skip := false
		for _, existing := range out {
			if pathHiddenByWhiteout(p, existing) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, p)
		}
	}
	return out
}

func sortTarEntries(entries []TreeEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		rank := func(e TreeEntry) int {
			switch e.Typeflag {
			case tar.TypeDir:
				return 0
			case tar.TypeReg, tar.TypeRegA:
				return 1
			case tar.TypeSymlink:
				return 2
			case tar.TypeLink:
				return 3
			default:
				return 1
			}
		}
		ri, rj := rank(entries[i]), rank(entries[j])
		if ri != rj {
			return ri < rj
		}
		return entries[i].Path < entries[j].Path
	})
}
