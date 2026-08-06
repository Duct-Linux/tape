package utils

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// This is the glibc crash.
//
// Replacing a shared library in place -- open(O_TRUNC) then write -- pulls the
// pages out from under every process that has it mmap'd. On a running system
// that means every process using libc segfaults at once, including the daemon
// performing the install. Package managers avoid this by writing a temporary
// file alongside the target and rename()ing over it: rename is atomic, and the
// old inode stays alive for anyone still holding it.
//
// The observable property is that the file's identity changes: a reader who
// opened the old file keeps seeing the old bytes.
func TestUpgradeReplacesFilesAtomically(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	const libPath = "usr/lib/libc.so.6"

	v1 := buildPkg(t, archives, "glibc", "2.36", nil, []pkgFile{
		{path: libPath, body: "GLIBC VERSION 2.36 TEXT SEGMENT", mode: 0755},
	})
	if _, err := InstallPkg(v1, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatal(err)
	}

	installed := filepath.Join(sysroot, libPath)

	// A running process holding the library open, as every process on a live
	// system would be.
	running, err := os.Open(installed)
	if err != nil {
		t.Fatal(err)
	}
	defer running.Close()

	beforeInfo, err := running.Stat()
	if err != nil {
		t.Fatal(err)
	}

	// Upgrade underneath it.
	v2 := buildPkg(t, archives, "glibc", "2.39", nil, []pkgFile{
		{path: libPath, body: "GLIBC VERSION 2.39 TEXT SEGMENT (much longer than before)", mode: 0755},
	})
	if _, err := InstallPkg(v2, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatal(err)
	}

	// The already-open handle must still see the original bytes in full. If the
	// installer truncated in place, this read comes back empty or garbled --
	// which is precisely what makes a running system fall over.
	if _, err := running.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	stillOpen, err := io.ReadAll(running)
	if err != nil {
		t.Fatalf("reading through the pre-existing handle: %v", err)
	}
	if string(stillOpen) != "GLIBC VERSION 2.36 TEXT SEGMENT" {
		t.Errorf("a process holding the old library saw %q;\n"+
			"the file was replaced in place instead of atomically, which is what "+
			"crashes every running process when glibc is upgraded", stillOpen)
	}

	// The path itself must show the new content.
	onDisk, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != "GLIBC VERSION 2.39 TEXT SEGMENT (much longer than before)" {
		t.Errorf("path content = %q, want the upgraded library", onDisk)
	}

	// And it must genuinely be a different file, not the same inode rewritten.
	afterInfo, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(beforeInfo, afterInfo) {
		t.Error("upgrade reused the same inode; running processes would have seen the change mid-execution")
	}
}

// The same guarantee for the file mode: a partially-written binary must never
// be observable at the target path.
func TestUpgradeNeverExposesAPartialFile(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	const binPath = "usr/bin/critical"

	v1 := buildPkg(t, archives, "tool", "1.0", nil, []pkgFile{
		{path: binPath, body: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", mode: 0755},
	})
	if _, err := InstallPkg(v1, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatal(err)
	}

	// A shorter replacement: with in-place truncation the target would briefly
	// be zero-length, and would keep stale tail bytes without O_TRUNC.
	v2 := buildPkg(t, archives, "tool", "2.0", nil, []pkgFile{
		{path: binPath, body: "BBBB", mode: 0755},
	})
	if _, err := InstallPkg(v2, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(sysroot, binPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "BBBB" {
		t.Errorf("content = %q, want %q", got, "BBBB")
	}

	fi, err := os.Stat(filepath.Join(sysroot, binPath))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0755 {
		t.Errorf("mode = %v, want 0755", fi.Mode().Perm())
	}
}

// Replacing a symlink must be atomic too: /usr/lib/libc.so.6 is a symlink on
// many systems, and an unlink-then-create window leaves it briefly absent.
func TestUpgradeReplacesSymlinksAtomically(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	v1 := buildPkg(t, archives, "libdemo", "1.0", nil, []pkgFile{
		{path: "usr/lib/libdemo.so.1", body: "v1"},
		{path: "usr/lib/libdemo.so", symlink: "libdemo.so.1"},
	})
	if _, err := InstallPkg(v1, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatal(err)
	}

	v2 := buildPkg(t, archives, "libdemo", "2.0", nil, []pkgFile{
		{path: "usr/lib/libdemo.so.2", body: "v2"},
		{path: "usr/lib/libdemo.so", symlink: "libdemo.so.2"},
	})
	if _, err := InstallPkg(v2, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatalf("replacing a symlink: %v", err)
	}

	target, err := os.Readlink(filepath.Join(sysroot, "usr/lib/libdemo.so"))
	if err != nil {
		t.Fatalf("symlink missing after upgrade: %v", err)
	}
	if target != "libdemo.so.2" {
		t.Errorf("link target = %q, want libdemo.so.2", target)
	}
}
