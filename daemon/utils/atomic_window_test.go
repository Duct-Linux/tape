package utils

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Changing the inode is necessary but not sufficient. Renaming the old file
// out of the way and *then* creating the new one leaves an interval where the
// path does not exist at all -- and a process that tries to exec or dlopen
// during that interval fails with ENOENT. For libc that is as fatal as
// truncation, just with a different error.
//
// A correct replacement writes a temporary file alongside the target and
// renames it over the top, so the path resolves to either the old file or the
// new one at every instant, and never to nothing.
func TestUpgradeNeverLeavesTheTargetMissing(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	const libPath = "usr/lib/libc.so.6"
	target := filepath.Join(sysroot, libPath)

	base := buildPkg(t, archives, "glibc", "2.36", nil, []pkgFile{
		{path: libPath, body: "v0", mode: 0755},
	})
	if _, err := InstallPkg(base, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatal(err)
	}

	// Pre-build the upgrades so the observer only races the install itself.
	const rounds = 40
	archivesForRound := make([]string, 0, rounds)
	for i := 0; i < rounds; i++ {
		version := "2." + strconv.Itoa(40+i)
		archivesForRound = append(archivesForRound, buildPkg(t, archives, "glibc", version, nil, []pkgFile{
			{path: libPath, body: "payload-" + version, mode: 0755},
		}))
	}

	var (
		missing atomic.Int64
		stop    atomic.Bool
		wg      sync.WaitGroup
	)

	// Stand in for every running process that needs libc present.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			if _, err := os.Lstat(target); err != nil && os.IsNotExist(err) {
				missing.Add(1)
			}
		}
	}()

	for _, archive := range archivesForRound {
		if _, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
			stop.Store(true)
			wg.Wait()
			t.Fatal(err)
		}
	}

	stop.Store(true)
	wg.Wait()

	if n := missing.Load(); n > 0 {
		t.Errorf("the library path vanished %d times during upgrades; "+
			"replacement is unlink-then-create rather than an atomic rename, so a "+
			"process starting mid-upgrade would fail to find libc", n)
	}
}

// Backups live beside their targets, so they must not survive a successful
// install -- otherwise every upgrade litters /usr/lib with hidden copies.
func TestUpgradeLeavesNoBackupFilesBehind(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	v1 := buildPkg(t, archives, "demo", "1.0", nil, []pkgFile{
		{path: "usr/lib/libdemo.so.1", body: "v1"},
		{path: "usr/lib/libdemo.so", symlink: "libdemo.so.1"},
		{path: "usr/bin/demo", body: "v1", mode: 0755},
	})
	if _, err := InstallPkg(v1, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatal(err)
	}

	v2 := buildPkg(t, archives, "demo", "2.0", nil, []pkgFile{
		{path: "usr/lib/libdemo.so.1", body: "v2-longer-content"},
		{path: "usr/lib/libdemo.so", symlink: "libdemo.so.1"},
		{path: "usr/bin/demo", body: "v2", mode: 0755},
	})
	if _, err := InstallPkg(v2, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatal(err)
	}

	var leftovers []string
	err := filepath.Walk(sysroot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		name := filepath.Base(path)
		if strings.HasPrefix(name, ".tape-") {
			leftovers = append(leftovers, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(leftovers) > 0 {
		t.Errorf("install left temporary/backup files behind: %v", leftovers)
	}
}

// A soname symlink must never dangle during an upgrade either. libc.so.6 points
// at libc-<version>.so, so if the link is repointed before the new target
// exists -- or the old target is swept while the link still refers to it --
// anything resolving the library fails just as hard as if it were missing.
//
// os.Stat follows symlinks, so this catches a dangling link that Lstat would
// happily report as present.
func TestUpgradeNeverLeavesASonameLinkDangling(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	link := filepath.Join(sysroot, "usr/lib/libc.so.6")

	v1 := buildPkg(t, archives, "glibc", "2.36", nil, []pkgFile{
		{path: "usr/lib/libc-2.36.so", body: "GLIBC 2.36"},
		{path: "usr/lib/libc.so.6", symlink: "libc-2.36.so"},
	})
	if _, err := InstallPkg(v1, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatal(err)
	}

	const rounds = 30
	upgrades := make([]string, 0, rounds)
	for i := 0; i < rounds; i++ {
		v := "2." + strconv.Itoa(40+i)
		upgrades = append(upgrades, buildPkg(t, archives, "glibc", v, nil, []pkgFile{
			{path: "usr/lib/libc-" + v + ".so", body: "GLIBC " + v},
			{path: "usr/lib/libc.so.6", symlink: "libc-" + v + ".so"},
		}))
	}

	var (
		broken atomic.Int64
		stop   atomic.Bool
		wg     sync.WaitGroup
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			// Follows the link: catches the link being gone *or* its target
			// missing. Only ENOENT counts -- darwin can transiently return
			// EINVAL for a path that is concurrently being renamed, which is a
			// platform artifact of this tight polling loop rather than a state
			// any real caller could observe as a missing file. Linux returns
			// the old or the new file and never EINVAL.
			if _, err := os.Stat(link); err != nil && os.IsNotExist(err) {
				broken.Add(1)
			}
		}
	}()

	for _, archive := range upgrades {
		if _, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
			stop.Store(true)
			wg.Wait()
			t.Fatal(err)
		}
	}

	stop.Store(true)
	wg.Wait()

	if n := broken.Load(); n > 0 {
		t.Errorf("libc.so.6 failed to resolve %d times during upgrades: the link was "+
			"repointed before its new target existed, or the old target was swept "+
			"while the link still referred to it", n)
	}
}
