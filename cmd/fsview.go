package cmd

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/mattn/go-isatty"

	"github.com/martinmares/oci-patch/internal/image"
)

const (
	ansiReset = "\x1b[0m"
	ansiBlue  = "\x1b[34m"
	ansiCyan  = "\x1b[36m"
	ansiGreen = "\x1b[32m"
)

func isTTYWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

func writeLS(w io.Writer, entries []image.TreeEntry, color bool) error {
	for _, entry := range entries {
		fmt.Fprintln(w, formatLSLine(entry, color))
	}
	return nil
}

func formatLSLine(entry image.TreeEntry, color bool) string {
	mode := entry.Mode
	if entry.IsDir && mode&os.ModeDir == 0 {
		mode |= os.ModeDir
	}
	if entry.Typeflag == tar.TypeSymlink {
		mode |= os.ModeSymlink
	}

	name := path.Base(entry.Path)
	if entry.Path == "/" {
		name = "/"
	}
	if entry.Typeflag == tar.TypeSymlink {
		name = fmt.Sprintf("%s -> %s", name, entry.Linkname)
	}

	name = colorizeName(name, entry, color)

	return fmt.Sprintf("%s %d:%d %8d %s %s",
		modeString(mode, entry),
		entry.UID,
		entry.GID,
		entrySize(entry),
		formatTime(entry.ModTime),
		name,
	)
}

func writeTree(w io.Writer, root string, entries []image.TreeEntry, color bool) error {
	if len(entries) == 0 {
		return nil
	}

	entryByPath := make(map[string]image.TreeEntry, len(entries))
	children := map[string][]image.TreeEntry{}
	for _, entry := range entries {
		entryByPath[entry.Path] = entry
	}
	for _, entry := range entries {
		if entry.Path == root {
			continue
		}
		parent := path.Dir(entry.Path)
		children[parent] = append(children[parent], entry)
	}

	for parent := range children {
		sort.Slice(children[parent], func(i, j int) bool {
			left := strings.ToLower(path.Base(children[parent][i].Path))
			right := strings.ToLower(path.Base(children[parent][j].Path))
			if left != right {
				return left < right
			}
			return children[parent][i].Path < children[parent][j].Path
		})
	}

	rootEntry, ok := entryByPath[root]
	if !ok {
		rootEntry = image.TreeEntry{Path: root, IsDir: true}
	}

	if !rootEntry.IsDir && rootEntry.Typeflag != tar.TypeDir {
		fmt.Fprintln(w, colorizeName(path.Base(rootEntry.Path), rootEntry, color))
		return nil
	}

	fmt.Fprintln(w, colorizeName(rootEntry.Path, rootEntry, color))
	return writeTreeChildren(w, root, "", children, color)
}

func writeTreeChildren(w io.Writer, parent, prefix string, children map[string][]image.TreeEntry, color bool) error {
	nodes := children[parent]
	for i, entry := range nodes {
		last := i == len(nodes)-1
		connector := "|-- "
		nextPrefix := prefix + "|   "
		if last {
			connector = "`-- "
			nextPrefix = prefix + "    "
		}
		fmt.Fprintf(w, "%s%s%s\n", prefix, connector, colorizeName(path.Base(entry.Path), entry, color))
		if entry.IsDir || entry.Typeflag == tar.TypeDir {
			if err := writeTreeChildren(w, entry.Path, nextPrefix, children, color); err != nil {
				return err
			}
		}
	}
	return nil
}

func entrySize(entry image.TreeEntry) int64 {
	switch entry.Typeflag {
	case tar.TypeSymlink:
		return int64(len(entry.Linkname))
	case tar.TypeDir:
		return 0
	default:
		return int64(len(entry.Data))
	}
}

func modeString(mode os.FileMode, entry image.TreeEntry) string {
	if entry.Typeflag == tar.TypeDir || entry.IsDir {
		mode |= os.ModeDir
	}
	if entry.Typeflag == tar.TypeSymlink || mode&os.ModeSymlink != 0 {
		mode |= os.ModeSymlink
	}
	return mode.String()
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "Jan  1 00:00"
	}
	return t.Format("Jan _2 15:04")
}

func colorizeName(name string, entry image.TreeEntry, color bool) string {
	if !color {
		return name
	}
	switch {
	case entry.Typeflag == tar.TypeDir || entry.IsDir:
		return ansiBlue + name + ansiReset
	case entry.Typeflag == tar.TypeSymlink || entry.Mode&os.ModeSymlink != 0:
		return ansiCyan + name + ansiReset
	case entry.Mode.Perm()&0o111 != 0 && (entry.Typeflag == tar.TypeReg || entry.Typeflag == tar.TypeRegA || entry.Typeflag == 0):
		return ansiGreen + name + ansiReset
	default:
		return name
	}
}
