package tarUtils

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// entriesOf lists what actually made it into the archive.
func entriesOf(t *testing.T, archive []byte) map[string]*tar.Header {
	t.Helper()

	gzr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	defer gzr.Close()

	out := map[string]*tar.Header{}
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		// Directory entries conventionally carry a trailing slash; key on the
		// bare path so lookups read naturally.
		out[strings.TrimSuffix(hdr.Name, "/")] = hdr
	}
}

// A shared library ships as a real file plus one or more symlinks
// (libfoo.so -> libfoo.so.1). Tar walked only regular files, so the links were
// silently dropped and the installed package was missing its soname links --
// which is exactly how a glibc package ends up broken.
func TestTarIncludesSymlinks(t *testing.T) {
	src := t.TempDir()

	if err := os.MkdirAll(filepath.Join(src, "usr/lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "usr/lib/libdemo.so.1.2.3"), []byte("ELF"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libdemo.so.1.2.3", filepath.Join(src, "usr/lib/libdemo.so.1")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libdemo.so.1", filepath.Join(src, "usr/lib/libdemo.so")); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Tar(src, &buf); err != nil {
		t.Fatalf("Tar: %v", err)
	}

	entries := entriesOf(t, buf.Bytes())

	for _, name := range []string{"usr/lib/libdemo.so.1.2.3", "usr/lib/libdemo.so.1", "usr/lib/libdemo.so"} {
		if _, ok := entries[name]; !ok {
			t.Errorf("%s missing from the archive", name)
		}
	}

	if hdr := entries["usr/lib/libdemo.so.1"]; hdr != nil {
		if hdr.Typeflag != tar.TypeSymlink {
			t.Errorf("libdemo.so.1 archived as type %q, want a symlink", hdr.Typeflag)
		}
		if hdr.Linkname != "libdemo.so.1.2.3" {
			t.Errorf("link target = %q, want libdemo.so.1.2.3", hdr.Linkname)
		}
	}
}

// Empty directories carry meaning in a package (/var/log/demo, /var/cache/demo).
func TestTarIncludesDirectories(t *testing.T) {
	src := t.TempDir()

	if err := os.MkdirAll(filepath.Join(src, "var/log/demo"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "usr/bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "usr/bin/demo"), []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Tar(src, &buf); err != nil {
		t.Fatalf("Tar: %v", err)
	}

	entries := entriesOf(t, buf.Bytes())

	hdr, ok := entries["var/log/demo"]
	if !ok {
		t.Fatal("empty directory var/log/demo was dropped from the archive")
	}
	if hdr.Typeflag != tar.TypeDir {
		t.Errorf("var/log/demo archived as type %q, want a directory", hdr.Typeflag)
	}
	if !strings.HasSuffix(hdr.Name, "/") {
		t.Errorf("directory entry %q should end with a slash by convention", hdr.Name)
	}
	if os.FileMode(hdr.Mode).Perm() != 0750 {
		t.Errorf("directory mode = %v, want 0750", os.FileMode(hdr.Mode).Perm())
	}
}

func TestTarPreservesFileModes(t *testing.T) {
	src := t.TempDir()

	if err := os.WriteFile(filepath.Join(src, "script.sh"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "data.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Tar(src, &buf); err != nil {
		t.Fatal(err)
	}

	entries := entriesOf(t, buf.Bytes())
	if got := os.FileMode(entries["script.sh"].Mode).Perm(); got != 0755 {
		t.Errorf("script.sh mode = %v, want 0755", got)
	}
	if got := os.FileMode(entries["data.txt"].Mode).Perm(); got != 0644 {
		t.Errorf("data.txt mode = %v, want 0644", got)
	}
}

// Two builds of identical trees should produce identical archives.
func TestTarIsDeterministic(t *testing.T) {
	build := func() []byte {
		src := t.TempDir()
		for _, name := range []string{"b.txt", "a.txt", "c.txt"} {
			if err := os.WriteFile(filepath.Join(src, name), []byte(name), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(filepath.Join(src, "sub"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "sub/z.txt"), []byte("z"), 0644); err != nil {
			t.Fatal(err)
		}

		var buf bytes.Buffer
		if err := Tar(src, &buf); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	// Compare the archives byte for byte, not just their entry names. An
	// earlier version of this test only checked that the same names appeared in
	// both, which it did happily while every entry carried the wall-clock mtime
	// of its own build -- so two builds of an identical tree produced two
	// different packages and nothing noticed.
	firstBytes := build()
	secondBytes := build()

	first := entriesOf(t, firstBytes)
	second := entriesOf(t, secondBytes)

	if len(first) != len(second) {
		t.Fatalf("entry counts differ: %d vs %d", len(first), len(second))
	}
	for name, hdr := range first {
		other, ok := second[name]
		if !ok {
			t.Errorf("%s present in one build but not the other", name)
			continue
		}
		if !hdr.ModTime.Equal(other.ModTime) {
			t.Errorf("%s mod times differ: %s vs %s", name, hdr.ModTime, other.ModTime)
		}
	}

	if !bytes.Equal(firstBytes, secondBytes) {
		t.Errorf("archives differ: %d bytes vs %d bytes", len(firstBytes), len(secondBytes))
	}
}

// SOURCE_DATE_EPOCH lets a distribution stamp its packages with the release
// date instead of 1970, without giving up reproducibility.
func TestTarHonoursSourceDateEpoch(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}

	build := func() map[string]*tar.Header {
		var buf bytes.Buffer
		if err := Tar(src, &buf); err != nil {
			t.Fatal(err)
		}
		return entriesOf(t, buf.Bytes())
	}

	t.Setenv("SOURCE_DATE_EPOCH", "1785542400")
	if got := build()["a.txt"].ModTime.UTC(); got.Unix() != 1785542400 {
		t.Errorf("mod time = %s, want the value from SOURCE_DATE_EPOCH", got)
	}

	// A value that is not an integer must not reintroduce wall-clock times.
	t.Setenv("SOURCE_DATE_EPOCH", "not-a-number")
	if got := build()["a.txt"].ModTime.UTC(); got.Unix() != 0 {
		t.Errorf("mod time = %s, want the epoch for a malformed SOURCE_DATE_EPOCH", got)
	}

	t.Setenv("SOURCE_DATE_EPOCH", "")
	if got := build()["a.txt"].ModTime.UTC(); got.Unix() != 0 {
		t.Errorf("mod time = %s, want the epoch when SOURCE_DATE_EPOCH is unset", got)
	}
}

// The source root must not appear as an entry, and names must be relative.
func TestTarNamesAreRelative(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "usr/bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "usr/bin/demo"), []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Tar(src, &buf); err != nil {
		t.Fatal(err)
	}

	for name := range entriesOf(t, buf.Bytes()) {
		if filepath.IsAbs(name) {
			t.Errorf("archive contains an absolute name: %q", name)
		}
		if name == "" || name == "." {
			t.Errorf("archive contains the source root itself: %q", name)
		}
	}
}

// A round trip through Tar and Untar must preserve the tree, links included.
func TestTarUntarRoundTrip(t *testing.T) {
	src := t.TempDir()

	if err := os.MkdirAll(filepath.Join(src, "usr/lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "usr/lib/libdemo.so.1"), []byte("ELF"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libdemo.so.1", filepath.Join(src, "usr/lib/libdemo.so")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "var/log/demo"), 0750); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Tar(src, &buf); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "out")
	opts := DefaultUntarOptions
	opts.AllowLinks = true
	if err := UntarWithOptions(dst, bytes.NewReader(buf.Bytes()), opts); err != nil {
		t.Fatalf("Untar: %v", err)
	}

	link, err := os.Readlink(filepath.Join(dst, "usr/lib/libdemo.so"))
	if err != nil {
		t.Fatalf("symlink did not survive the round trip: %v", err)
	}
	if link != "libdemo.so.1" {
		t.Errorf("link target = %q, want libdemo.so.1", link)
	}

	if fi, err := os.Stat(filepath.Join(dst, "var/log/demo")); err != nil {
		t.Errorf("empty directory did not survive the round trip: %v", err)
	} else if !fi.IsDir() {
		t.Error("var/log/demo came back as something other than a directory")
	}
}
