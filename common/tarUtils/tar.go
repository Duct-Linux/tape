package tarUtils

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// zeroTime normalises timestamps that would otherwise vary between builds.
var zeroTime = time.Unix(0, 0).UTC()

// archiveModTime is the modification time stamped into every entry.
//
// It has to be a fixed value rather than the file's own mtime, which is just
// whenever the build happened to run -- that alone made two builds of an
// identical tree produce different archives.
//
// SOURCE_DATE_EPOCH is the conventional way for a distribution to pin that
// value, so packages carry a date that means something (the release) instead of
// 1970. A malformed value falls back to the epoch: still deterministic, which
// is the property that matters here.
func archiveModTime() time.Time {
	v := os.Getenv("SOURCE_DATE_EPOCH")
	if v == "" {
		return zeroTime
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return zeroTime
	}
	return time.Unix(secs, 0).UTC()
}

// Tar walks src and writes it as a gzipped tar to each of writers.
//
// Directories, regular files and symlinks are all archived. Symlinks matter: a
// shared library ships as a real file plus its soname links
// (libfoo.so -> libfoo.so.1), and an earlier version of this function walked
// only regular files, so those links were silently dropped -- producing a
// package that installs a library nothing can link against.
//
// Entries are emitted in sorted order with normalised metadata -- ownership
// dropped, timestamps fixed (see archiveModTime) -- so building the same tree
// twice produces a byte-identical archive.
func Tar(src string, writers ...io.Writer) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("unable to tar files - %v", err.Error())
	}
	if !info.IsDir() {
		return fmt.Errorf("unable to tar files - %s is not a directory", src)
	}

	mw := io.MultiWriter(writers...)

	gzw := gzip.NewWriter(mw)
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Close()

	paths, err := collectPaths(src)
	if err != nil {
		return err
	}

	// Resolved once so every entry in an archive carries the same timestamp
	// even if the environment changes underneath a long build.
	modTime := archiveModTime()

	// Tracks the first archive name seen for each inode, so the second and
	// later links to it are stored as links rather than as further copies of
	// the same bytes.
	seen := make(map[inodeKey]string)

	for _, path := range paths {
		if err := writeTarEntry(tw, src, path, modTime, seen); err != nil {
			return err
		}
	}

	return nil
}

// inodeKey identifies a file on disk, so two paths sharing one inode can be
// recognised as the same file.
type inodeKey struct {
	dev uint64
	ino uint64
}

// collectPaths gathers every path under src, sorted, excluding src itself.
func collectPaths(src string) ([]string, error) {
	var paths []string

	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == src {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sorting gives a stable archive regardless of directory iteration order,
	// and guarantees a parent is written before its children.
	sort.Strings(paths)

	return paths, nil
}

func writeTarEntry(tw *tar.Writer, src, path string, modTime time.Time, seen map[inodeKey]string) error {
	// Lstat, not Stat: a symlink must be recorded as a link rather than
	// followed and stored as a copy of whatever it points at.
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}

	name, err := filepath.Rel(src, path)
	if err != nil {
		return err
	}
	name = filepath.ToSlash(name)
	if name == "" || name == "." {
		return nil
	}

	var linkTarget string
	if fi.Mode()&os.ModeSymlink != 0 {
		linkTarget, err = os.Readlink(path)
		if err != nil {
			return err
		}
	}

	header, err := tar.FileInfoHeader(fi, linkTarget)
	if err != nil {
		return err
	}
	header.Name = name

	// Normalise metadata that would otherwise vary between builds of an
	// identical tree. Ownership is deliberately dropped: a package should not
	// carry the uid of whoever happened to build it.
	header.Uid, header.Gid = 0, 0
	header.Uname, header.Gname = "", ""
	header.AccessTime, header.ChangeTime = zeroTime, zeroTime
	header.ModTime = modTime

	// A hard link is one file under several names. The walk sees each name
	// separately, and storing every one as a regular file writes the contents
	// once per name: uutils installs 107 links to a single 14 MB binary, which
	// came out as a 643 MB package for 14 MB of program.
	//
	// The first name encountered carries the payload; the rest become
	// TypeLink entries pointing at it. Sorting in collectPaths makes which
	// name comes first deterministic, so the archive stays reproducible.
	if fi.Mode().IsRegular() {
		if key, ok := inodeOf(fi); ok {
			if target, dup := seen[key]; dup {
				header.Typeflag = tar.TypeLink
				header.Linkname = target
				header.Size = 0
				return tw.WriteHeader(header)
			}
			seen[key] = name
		}
	}

	switch {
	case fi.IsDir():
		header.Name = strings.TrimSuffix(name, "/") + "/"
		header.Size = 0

	case fi.Mode()&os.ModeSymlink != 0:
		header.Size = 0

	case fi.Mode().IsRegular():
		// Size comes from FileInfoHeader.

	default:
		// Device nodes, sockets and FIFOs. Silently skipping unusual types is
		// how the old implementation lost symlinks; be explicit instead.
		return fmt.Errorf("cannot archive %s: unsupported file type %v", name, fi.Mode().Type())
	}

	if err := tw.WriteHeader(header); err != nil {
		return err
	}

	if !fi.Mode().IsRegular() {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	// Closed explicitly rather than deferred: deferring inside a loop would
	// hold every file open until the whole archive is written.
	written, err := io.Copy(tw, f)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if written != header.Size {
		return fmt.Errorf("%s changed size while being archived", name)
	}

	return nil
}
