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

type entry struct {
	name     string
	typeflag byte
	mode     int64
	body     string
	linkname string
}

func makeTarGz(t *testing.T, entries []entry) io.Reader {
	t.Helper()

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		mode := e.mode
		if mode == 0 {
			mode = 0644
		}
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: typeflag,
			Mode:     mode,
			Size:     int64(len(e.body)),
			Linkname: e.linkname,
		}
		if typeflag == tar.TypeDir || typeflag == tar.TypeSymlink || typeflag == tar.TypeLink {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader(%q): %v", e.name, err)
		}
		if hdr.Size > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("Write(%q): %v", e.name, err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

// dstDir returns a fresh extraction target nested inside a scratch parent, so
// that a successful escape is observable in the parent.
func dstDir(t *testing.T) (parent, dst string) {
	t.Helper()
	parent = t.TempDir()
	dst = filepath.Join(parent, "extract")
	if err := os.MkdirAll(dst, 0755); err != nil {
		t.Fatal(err)
	}
	return parent, dst
}

func TestUntarRejectsParentTraversal(t *testing.T) {
	parent, dst := dstDir(t)

	r := makeTarGz(t, []entry{{name: "../escaped.txt", body: "pwned"}})

	if err := Untar(dst, r); err == nil {
		t.Fatal("Untar accepted a ../ traversal entry, want error")
	}

	if _, err := os.Stat(filepath.Join(parent, "escaped.txt")); err == nil {
		t.Fatal("traversal entry escaped the destination directory")
	}
}

func TestUntarRejectsDeepParentTraversal(t *testing.T) {
	parent, dst := dstDir(t)

	r := makeTarGz(t, []entry{{name: "a/b/../../../escaped.txt", body: "pwned"}})

	if err := Untar(dst, r); err == nil {
		t.Fatal("Untar accepted a nested ../ traversal entry, want error")
	}
	if _, err := os.Stat(filepath.Join(parent, "escaped.txt")); err == nil {
		t.Fatal("traversal entry escaped the destination directory")
	}
}

func TestUntarRejectsAbsolutePath(t *testing.T) {
	_, dst := dstDir(t)

	target := filepath.Join(t.TempDir(), "absolute-escape.txt")
	r := makeTarGz(t, []entry{{name: target, body: "pwned"}})

	if err := Untar(dst, r); err == nil {
		t.Fatal("Untar accepted an absolute path entry, want error")
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("absolute path entry escaped the destination directory")
	}
}

// A symlink pointing outside the destination, followed by a write "through" it.
// This is the attack that O_NOFOLLOW on the final component alone does not stop.
func TestUntarRejectsSymlinkEscape(t *testing.T) {
	parent, dst := dstDir(t)

	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}

	r := makeTarGz(t, []entry{
		{name: "link", typeflag: tar.TypeSymlink, linkname: outside},
		{name: "link/pwned.txt", body: "pwned"},
	})

	if err := Untar(dst, r); err == nil {
		t.Fatal("Untar accepted a symlink escape, want error")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.txt")); err == nil {
		t.Fatal("write escaped through a symlink")
	}
}

func TestUntarRejectsAbsoluteSymlinkTarget(t *testing.T) {
	_, dst := dstDir(t)

	r := makeTarGz(t, []entry{
		{name: "evil", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
	})

	if err := Untar(dst, r); err == nil {
		t.Fatal("Untar accepted an absolute symlink target, want error")
	}
}

func TestUntarRejectsRelativeSymlinkEscape(t *testing.T) {
	_, dst := dstDir(t)

	r := makeTarGz(t, []entry{
		{name: "evil", typeflag: tar.TypeSymlink, linkname: "../../etc/passwd"},
	})

	if err := Untar(dst, r); err == nil {
		t.Fatal("Untar accepted a symlink escaping via .., want error")
	}
}

// Without O_TRUNC, writing a short file over a longer existing one leaves the
// old tail bytes in place.
func TestUntarTruncatesExistingFile(t *testing.T) {
	_, dst := dstDir(t)

	existing := filepath.Join(dst, "file.txt")
	if err := os.WriteFile(existing, []byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAA"), 0644); err != nil {
		t.Fatal(err)
	}

	r := makeTarGz(t, []entry{{name: "file.txt", body: "short"}})
	if err := Untar(dst, r); err != nil {
		t.Fatalf("Untar: %v", err)
	}

	got, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "short" {
		t.Errorf("file content = %q, want %q (stale bytes left behind)", got, "short")
	}
}

func TestUntarStripsSetuidBits(t *testing.T) {
	_, dst := dstDir(t)

	// 04755: setuid root binary smuggled in via the archive's mode bits.
	r := makeTarGz(t, []entry{{name: "sudo", mode: 04755, body: "#!/bin/sh\n"}})
	if err := Untar(dst, r); err != nil {
		t.Fatalf("Untar: %v", err)
	}

	fi, err := os.Stat(filepath.Join(dst, "sudo"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSetuid != 0 {
		t.Error("setuid bit survived extraction")
	}
	if fi.Mode()&os.ModeSetgid != 0 {
		t.Error("setgid bit survived extraction")
	}
}

func TestUntarEnforcesEntryLimit(t *testing.T) {
	_, dst := dstDir(t)

	entries := make([]entry, 0, 64)
	for i := 0; i < 64; i++ {
		entries = append(entries, entry{name: "f" + strings.Repeat("x", i), body: "y"})
	}

	err := UntarWithOptions(dst, makeTarGz(t, entries), UntarOptions{
		MaxEntries:    10,
		MaxTotalBytes: 1 << 20,
	})
	if err == nil {
		t.Fatal("Untar accepted more entries than MaxEntries, want error")
	}
}

func TestUntarEnforcesSizeLimit(t *testing.T) {
	_, dst := dstDir(t)

	r := makeTarGz(t, []entry{{name: "big.bin", body: strings.Repeat("A", 4096)}})

	err := UntarWithOptions(dst, r, UntarOptions{
		MaxEntries:    100,
		MaxTotalBytes: 1024,
	})
	if err == nil {
		t.Fatal("Untar accepted more bytes than MaxTotalBytes, want error")
	}
}

func TestUntarHappyPath(t *testing.T) {
	_, dst := dstDir(t)

	r := makeTarGz(t, []entry{
		{name: "TAPEPACKAGE.toml", body: "[package]\nname = \"demo\"\n"},
		{name: "install", typeflag: tar.TypeDir, mode: 0755},
		{name: "install/usr", typeflag: tar.TypeDir, mode: 0755},
		{name: "install/usr/bin", typeflag: tar.TypeDir, mode: 0755},
		{name: "install/usr/bin/demo", mode: 0755, body: "#!/bin/sh\necho demo\n"},
	})

	if err := Untar(dst, r); err != nil {
		t.Fatalf("Untar: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "install/usr/bin/demo"))
	if err != nil {
		t.Fatalf("expected extracted file: %v", err)
	}
	if string(got) != "#!/bin/sh\necho demo\n" {
		t.Errorf("unexpected content %q", got)
	}

	fi, err := os.Stat(filepath.Join(dst, "install/usr/bin/demo"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0100 == 0 {
		t.Error("executable bit was not preserved")
	}

	cfg, err := os.ReadFile(filepath.Join(dst, "TAPEPACKAGE.toml"))
	if err != nil {
		t.Fatalf("expected package config: %v", err)
	}
	if !strings.Contains(string(cfg), "demo") {
		t.Errorf("unexpected config content %q", cfg)
	}
}

// Regular files must land even when the archive omits explicit directory
// entries for their parents -- Tar() only walks regular files.
func TestUntarCreatesMissingParents(t *testing.T) {
	_, dst := dstDir(t)

	r := makeTarGz(t, []entry{{name: "deep/nested/path/file.txt", body: "ok"}})
	if err := Untar(dst, r); err != nil {
		t.Fatalf("Untar: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "deep/nested/path/file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ok" {
		t.Errorf("content = %q, want %q", got, "ok")
	}
}

// Device nodes and FIFOs have no business in a package payload.
func TestUntarRejectsDeviceNodes(t *testing.T) {
	_, dst := dstDir(t)

	r := makeTarGz(t, []entry{
		{name: "evil-dev", typeflag: tar.TypeChar},
	})
	if err := Untar(dst, r); err == nil {
		t.Fatal("Untar accepted a character device entry, want error")
	}
}

// A colon is an ordinary character in a POSIX filename. perl ships manual pages
// called App::Cpan.3, and rejecting every colon made perl unpackageable -- while
// doing nothing for security, since the danger is a drive *prefix*, not the
// character.
func TestSecureJoinAllowsColonsInNames(t *testing.T) {
	base := t.TempDir()

	for _, name := range []string{
		"usr/share/man/man3/App::Cpan.3",
		"usr/share/man/man3/Pod::Simple::Text.3",
		// A single-letter perl module: "B:" is shaped exactly like a drive
		// letter, and is why the check has to care about position.
		"usr/share/man/man3/B::Concise.3",
		"weird:name",
		// Relative on Windows too, so not a volume path.
		"sub/D:/notabsolute",
	} {
		if _, err := secureJoin(base, name); err != nil {
			t.Errorf("secureJoin(%q) = %v, want it accepted", name, err)
		}
	}
}

// The protection that colon check was really for: a Windows drive specifier is
// an absolute path there, so it must still be refused regardless of host.
func TestSecureJoinRejectsVolumePaths(t *testing.T) {
	base := t.TempDir()

	for _, name := range []string{
		`C:\Windows\evil`,
		"C:/Windows/evil",
		`x:\evil`,
	} {
		if _, err := secureJoin(base, name); err == nil {
			t.Errorf("secureJoin(%q) = nil, want it rejected", name)
		}
	}
}
