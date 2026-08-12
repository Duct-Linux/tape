package utils

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"tape/common/arch"
	"tape/common/database"
	"testing"
)

// --- fixtures ----------------------------------------------------------------

type pkgFile struct {
	path     string // relative to the payload dir, e.g. "usr/bin/demo"
	body     string
	mode     int64
	symlink  string // if set, entry is a symlink with this target
	rawEntry bool   // if set, path is used verbatim (outside "install/")
	isDir    bool   // if set, entry is a directory
}

// buildPkg writes a .tape.tar.gz with the layout builder produces:
// TAPEPACKAGE.toml at the root and the install tree under "install/".
func buildPkg(t *testing.T, dir, name, version string, deps map[string]string, files []pkgFile) string {
	t.Helper()

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	var depLines strings.Builder
	for depName, constraint := range deps {
		depLines.WriteString(depName + " = '" + constraint + "'\n")
	}

	// Stamp the architecture this test binary is running on, so the suite is
	// not tied to the machine that happens to run it -- and so the arch check
	// under test is exercised rather than accidentally tripped.
	manifest := "[dependencies]\n" + depLines.String() +
		"\n[package]\narch = '" + arch.Current() + "'\nname = '" + name + "'\n" +
		"subversion = '1'\nversion = '" + version + "'\n"

	writeEntry := func(path string, body string, mode int64, link string, isDir bool) {
		t.Helper()
		hdr := &tar.Header{Name: path, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if isDir {
			hdr.Typeflag = tar.TypeDir
			hdr.Size = 0
		}
		if link != "" {
			hdr.Typeflag = tar.TypeSymlink
			hdr.Linkname = link
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Size > 0 {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}

	writeEntry("TAPEPACKAGE.toml", manifest, 0644, "", false)
	for _, f := range files {
		path := f.path
		if !f.rawEntry {
			path = payloadDir + "/" + f.path
		}
		mode := f.mode
		if mode == 0 {
			mode = 0644
		}
		writeEntry(path, f.body, mode, f.symlink, f.isDir)
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(dir, name+"-"+version+"-1."+arch.Current()+".tape.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	return archive
}

func testEnv(t *testing.T) (sysroot string, archiveDir string, db *database.InstalledDB) {
	t.Helper()

	root := t.TempDir()
	sysroot = filepath.Join(root, "sysroot")
	archiveDir = filepath.Join(root, "archives")
	for _, d := range []string{sysroot, archiveDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	db, err := database.OpenInstalledDB(filepath.Join(root, "installed.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	return sysroot, archiveDir, db
}

// --- tests -------------------------------------------------------------------

func TestInstallPlacesFilesAndRecordsThem(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	archive := buildPkg(t, archives, "demo", "1.0", map[string]string{"libdemo": "1.0"}, []pkgFile{
		{path: "usr/bin/demo", body: "#!/bin/sh\necho demo\n", mode: 0755},
		{path: "usr/share/demo/data.txt", body: "payload"},
	})

	pkg, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, nil)
	if err != nil {
		t.Fatalf("InstallPkg: %v", err)
	}
	if pkg.Name != "demo" || pkg.Version != "1.0" {
		t.Errorf("unexpected package record %+v", pkg)
	}

	// Files landed.
	got, err := os.ReadFile(filepath.Join(sysroot, "usr/bin/demo"))
	if err != nil {
		t.Fatalf("expected installed binary: %v", err)
	}
	if string(got) != "#!/bin/sh\necho demo\n" {
		t.Errorf("unexpected content %q", got)
	}

	fi, err := os.Stat(filepath.Join(sysroot, "usr/bin/demo"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0100 == 0 {
		t.Error("executable bit not preserved through install")
	}

	// And are recorded, with hashes.
	files, err := db.Files("demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("recorded %d files, want 2", len(files))
	}
	for _, f := range files {
		if f.Sha256 == "" {
			t.Errorf("no hash recorded for %s", f.Path)
		}
	}

	// Dependency edges survive, so orphan detection works later.
	dependents, err := db.RequiredBy("libdemo")
	if err != nil {
		t.Fatal(err)
	}
	if len(dependents) != 1 || dependents[0] != "demo" {
		t.Errorf("RequiredBy(libdemo) = %v, want [demo]", dependents)
	}
}

// The manifest must never contain TAPEPACKAGE.toml itself -- only the payload.
func TestInstallDoesNotInstallPackageMetadata(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	archive := buildPkg(t, archives, "demo", "1.0", nil, []pkgFile{
		{path: "usr/bin/demo", body: "x", mode: 0755},
	})
	if _, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(sysroot, "TAPEPACKAGE.toml")); err == nil {
		t.Error("package metadata was installed into the sysroot")
	}
}

// The dev fixtures are metadata-only; installing one must succeed, not crash.
func TestInstallMetadataOnlyPackage(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	archive := buildPkg(t, archives, "dep-1", "1.0", map[string]string{"dep-1-1": "1.0"}, nil)

	if _, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatalf("InstallPkg on a payload-less package: %v", err)
	}
	installed, err := db.IsInstalled("dep-1")
	if err != nil || !installed {
		t.Errorf("metadata-only package not recorded (installed=%v, err=%v)", installed, err)
	}
}

func TestInstallPreservesSymlinks(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	archive := buildPkg(t, archives, "libdemo", "1.0", nil, []pkgFile{
		{path: "usr/lib/libdemo.so.1", body: "ELF"},
		{path: "usr/lib/libdemo.so", symlink: "libdemo.so.1"},
	})

	if _, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatalf("InstallPkg: %v", err)
	}

	link := filepath.Join(sysroot, "usr/lib/libdemo.so")
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("symlink not installed: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink was installed as a regular file")
	}
	target, err := os.Readlink(link)
	if err != nil || target != "libdemo.so.1" {
		t.Errorf("link target = %q (err %v), want %q", target, err, "libdemo.so.1")
	}
}

// Two packages claiming the same path must be refused, and the second install
// must leave the first one's file untouched.
func TestInstallRefusesFileConflict(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	first := buildPkg(t, archives, "alpha", "1.0", nil, []pkgFile{
		{path: "usr/bin/shared", body: "from alpha", mode: 0755},
	})
	if _, err := InstallPkg(first, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatal(err)
	}

	second := buildPkg(t, archives, "beta", "1.0", nil, []pkgFile{
		{path: "usr/bin/shared", body: "from beta", mode: 0755},
	})
	_, err := InstallPkg(second, InstallOptions{Sysroot: sysroot}, db, nil)
	if err == nil {
		t.Fatal("InstallPkg accepted a conflicting file, want error")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("error %q does not mention the conflict", err)
	}

	got, _ := os.ReadFile(filepath.Join(sysroot, "usr/bin/shared"))
	if string(got) != "from alpha" {
		t.Errorf("conflicting install modified the existing file: %q", got)
	}
	if installed, _ := db.IsInstalled("beta"); installed {
		t.Error("rejected package was still recorded as installed")
	}
}

// Reinstalling the same package is not a conflict with itself.
func TestReinstallReplacesCleanly(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	v1 := buildPkg(t, archives, "demo", "1.0", nil, []pkgFile{
		{path: "usr/bin/demo", body: "v1", mode: 0755},
		{path: "usr/share/gone.txt", body: "removed in v2"},
	})
	if _, err := InstallPkg(v1, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatal(err)
	}

	v2 := buildPkg(t, archives, "demo", "2.0", nil, []pkgFile{
		{path: "usr/bin/demo", body: "v2", mode: 0755},
	})
	if _, err := InstallPkg(v2, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatalf("reinstall: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(sysroot, "usr/bin/demo"))
	if string(got) != "v2" {
		t.Errorf("file not updated: %q", got)
	}

	pkg, _ := db.Get("demo")
	if pkg.Version != "2.0" {
		t.Errorf("version = %q, want 2.0", pkg.Version)
	}

	files, _ := db.Files("demo")
	if len(files) != 1 {
		t.Errorf("stale manifest rows survived the upgrade: %+v", files)
	}

	// A file the old version shipped and the new one dropped must be deleted
	// from disk too, not just forgotten by the database -- otherwise every
	// upgrade leaves unowned files behind that nothing will ever clean up.
	if _, err := os.Stat(filepath.Join(sysroot, "usr/share/gone.txt")); err == nil {
		t.Error("file dropped by the new version was left on disk")
	}
}

// A payload that escapes the sysroot must be refused outright.
func TestInstallRefusesPayloadEscape(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	archive := buildPkg(t, archives, "evil", "1.0", nil, []pkgFile{
		{path: "install/../../../../etc/cron.d/pwn", body: "pwned", rawEntry: true},
	})

	if _, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, nil); err == nil {
		t.Fatal("InstallPkg accepted a traversing payload entry, want error")
	}
	if installed, _ := db.IsInstalled("evil"); installed {
		t.Error("traversing package was recorded as installed")
	}
}

func TestRemoveDeletesFilesAndRecord(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	archive := buildPkg(t, archives, "demo", "1.0", nil, []pkgFile{
		{path: "usr/bin/demo", body: "x", mode: 0755},
		{path: "usr/share/demo/data.txt", body: "y"},
	})
	if _, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatal(err)
	}

	result, err := RemovePkg("demo", RemoveOptions{Sysroot: sysroot}, db)
	if err != nil {
		t.Fatalf("RemovePkg: %v", err)
	}
	if result.FilesRemoved != 2 {
		t.Errorf("FilesRemoved = %d, want 2", result.FilesRemoved)
	}

	for _, p := range []string{"usr/bin/demo", "usr/share/demo/data.txt"} {
		if _, err := os.Stat(filepath.Join(sysroot, p)); err == nil {
			t.Errorf("%s survived removal", p)
		}
	}
	// Empty directories are pruned...
	if _, err := os.Stat(filepath.Join(sysroot, "usr/share/demo")); err == nil {
		t.Error("empty directory was not pruned")
	}
	// ...but never the sysroot itself.
	if _, err := os.Stat(sysroot); err != nil {
		t.Errorf("sysroot was removed: %v", err)
	}

	if installed, _ := db.IsInstalled("demo"); installed {
		t.Error("package still recorded after removal")
	}
}

// A directory shared with another package must survive removal.
func TestRemoveKeepsSharedDirectories(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	a := buildPkg(t, archives, "alpha", "1.0", nil, []pkgFile{{path: "usr/bin/alpha", body: "a", mode: 0755}})
	b := buildPkg(t, archives, "beta", "1.0", nil, []pkgFile{{path: "usr/bin/beta", body: "b", mode: 0755}})
	for _, archive := range []string{a, b} {
		if _, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := RemovePkg("alpha", RemoveOptions{Sysroot: sysroot}, db); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(sysroot, "usr/bin/beta")); err != nil {
		t.Errorf("other package's file was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sysroot, "usr/bin")); err != nil {
		t.Errorf("shared directory was pruned while still in use: %v", err)
	}
}

// A file edited since installation is local state, not ours to delete.
func TestRemoveKeepsModifiedFiles(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	archive := buildPkg(t, archives, "demo", "1.0", nil, []pkgFile{
		{path: "etc/demo.conf", body: "original"},
	})
	if _, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatal(err)
	}

	conf := filepath.Join(sysroot, "etc/demo.conf")
	if err := os.WriteFile(conf, []byte("locally edited"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := RemovePkg("demo", RemoveOptions{Sysroot: sysroot}, db)
	if err != nil {
		t.Fatalf("RemovePkg: %v", err)
	}
	if result.FilesRemoved != 0 {
		t.Errorf("FilesRemoved = %d, want 0 (file was modified)", result.FilesRemoved)
	}

	got, err := os.ReadFile(conf)
	if err != nil {
		t.Fatalf("modified file was deleted: %v", err)
	}
	if string(got) != "locally edited" {
		t.Errorf("content = %q, want the local edit preserved", got)
	}
}

func TestRemoveRefusesWhenStillRequired(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	lib := buildPkg(t, archives, "libdemo", "1.0", nil, []pkgFile{{path: "usr/lib/libdemo.so", body: "x"}})
	app := buildPkg(t, archives, "demo", "1.0", map[string]string{"libdemo": "1.0"}, []pkgFile{{path: "usr/bin/demo", body: "y", mode: 0755}})
	for _, a := range []string{lib, app} {
		if _, err := InstallPkg(a, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := RemovePkg("libdemo", RemoveOptions{Sysroot: sysroot}, db); err == nil {
		t.Fatal("RemovePkg removed a package that is still required, want error")
	}
	if _, err := os.Stat(filepath.Join(sysroot, "usr/lib/libdemo.so")); err != nil {
		t.Error("required package's file was removed anyway")
	}

	// --force overrides.
	if _, err := RemovePkg("libdemo", RemoveOptions{Sysroot: sysroot, Force: true}, db); err != nil {
		t.Fatalf("forced removal failed: %v", err)
	}
}

func TestRemoveReportsOrphans(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	lib := buildPkg(t, archives, "libdemo", "1.0", nil, []pkgFile{{path: "usr/lib/libdemo.so", body: "x"}})
	if _, err := InstallPkg(lib, InstallOptions{Sysroot: sysroot, Reason: database.ReasonDependency}, db, nil); err != nil {
		t.Fatal(err)
	}
	app := buildPkg(t, archives, "demo", "1.0", map[string]string{"libdemo": "1.0"}, []pkgFile{{path: "usr/bin/demo", body: "y", mode: 0755}})
	if _, err := InstallPkg(app, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatal(err)
	}

	result, err := RemovePkg("demo", RemoveOptions{Sysroot: sysroot}, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Orphans) != 1 || result.Orphans[0] != "libdemo" {
		t.Errorf("Orphans = %v, want [libdemo]", result.Orphans)
	}
}

func TestRemoveMissingPackage(t *testing.T) {
	sysroot, _, db := testEnv(t)

	_, err := RemovePkg("nope", RemoveOptions{Sysroot: sysroot}, db)
	if !errors.Is(err, database.ErrNotInstalled) {
		t.Errorf("RemovePkg(missing) = %v, want ErrNotInstalled", err)
	}
}

func TestInstallReportsProgress(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	archive := buildPkg(t, archives, "demo", "1.0", nil, []pkgFile{
		{path: "usr/bin/demo", body: "x", mode: 0755},
		{path: "usr/bin/demo2", body: "y", mode: 0755},
	})

	var seen []int8
	if _, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, func(p int8) {
		seen = append(seen, p)
	}); err != nil {
		t.Fatal(err)
	}

	if len(seen) == 0 {
		t.Fatal("no progress reported")
	}
	for _, p := range seen {
		if p < 0 || p > 100 {
			t.Errorf("progress %d out of range", p)
		}
	}
	if seen[len(seen)-1] != 100 {
		t.Errorf("final progress = %d, want 100", seen[len(seen)-1])
	}
}

// Empty directories are real package artifacts (/var/log/demo, /var/cache/demo)
// and must be created even though nothing inside them triggers it.
func TestInstallCreatesEmptyPayloadDirectories(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	// buildPkg writes explicit entries, so add the directory by hand.
	archive := buildPkg(t, archives, "demo", "1.0", nil, []pkgFile{
		{path: "install/var/log/demo/", mode: 0750, rawEntry: true, isDir: true},
		{path: "usr/bin/demo", body: "x", mode: 0755},
	})

	if _, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatalf("InstallPkg: %v", err)
	}

	fi, err := os.Stat(filepath.Join(sysroot, "var/log/demo"))
	if err != nil {
		t.Fatalf("empty payload directory was not created: %v", err)
	}
	if !fi.IsDir() {
		t.Error("var/log/demo is not a directory")
	}

	// Directories are shared, so they must not appear in the manifest -- two
	// packages both shipping /usr/bin would otherwise collide.
	files, err := db.Files("demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path == "var/log/demo" {
			t.Error("directory was recorded as an owned file")
		}
	}
}

// Two packages shipping the same directory must both install.
func TestTwoPackagesCanShareADirectory(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	a := buildPkg(t, archives, "alpha", "1.0", nil, []pkgFile{
		{path: "install/usr/share/common/", mode: 0755, rawEntry: true, isDir: true},
		{path: "usr/share/common/a.txt", body: "a"},
	})
	b := buildPkg(t, archives, "beta", "1.0", nil, []pkgFile{
		{path: "install/usr/share/common/", mode: 0755, rawEntry: true, isDir: true},
		{path: "usr/share/common/b.txt", body: "b"},
	})

	for _, archive := range []string{a, b} {
		if _, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
			t.Fatalf("installing a package sharing a directory: %v", err)
		}
	}

	for _, p := range []string{"usr/share/common/a.txt", "usr/share/common/b.txt"} {
		if _, err := os.Stat(filepath.Join(sysroot, p)); err != nil {
			t.Errorf("%s missing: %v", p, err)
		}
	}
}

// buildPkgForArch stamps an explicit architecture, to exercise the check that
// keeps foreign binaries off the system.
func buildPkgForArch(t *testing.T, dir, name, version, pkgArch string) string {
	t.Helper()

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	manifest := "[dependencies]\n\n[package]\narch = '" + pkgArch + "'\nname = '" + name +
		"'\nsubversion = '1'\nversion = '" + version + "'\n"

	write := func(path, body string, mode int64) {
		hdr := &tar.Header{Name: path, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	write("TAPEPACKAGE.toml", manifest, 0644)
	write("install/usr/bin/"+name, "binary for "+pkgArch, 0755)

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(dir, name+"-"+version+"-1."+pkgArch+".tape.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	return archive
}

// Installing a package built for another architecture would put binaries on the
// system that cannot execute -- and if it replaced a system library, would
// leave a machine that no longer boots.
func TestInstallRefusesForeignArchitecture(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	// Pick an architecture that is definitely not this one.
	foreign := arch.X86_64
	if arch.Current() == arch.X86_64 {
		foreign = arch.Aarch64
	}

	archive := buildPkgForArch(t, archives, "demo", "1.0", foreign)

	_, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, nil)
	if err == nil {
		t.Fatalf("InstallPkg accepted a %s package on %s", foreign, arch.Current())
	}
	if !strings.Contains(err.Error(), foreign) || !strings.Contains(err.Error(), arch.Current()) {
		t.Errorf("error %q should name both the package and system architectures", err)
	}

	if _, statErr := os.Stat(filepath.Join(sysroot, "usr/bin/demo")); statErr == nil {
		t.Error("a foreign-architecture binary was installed anyway")
	}
	if installed, _ := db.IsInstalled("demo"); installed {
		t.Error("a foreign-architecture package was recorded as installed")
	}
}

// Architecture-independent packages install anywhere.
func TestInstallAcceptsAnyArchitecture(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	archive := buildPkgForArch(t, archives, "docs", "1.0", arch.Any)

	if _, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatalf("InstallPkg refused an \"any\" package: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sysroot, "usr/bin/docs")); err != nil {
		t.Errorf("\"any\" package was not installed: %v", err)
	}
}

// The system's own architecture, spelled differently, must still install: the
// index may say "arm64" where the running system reports "aarch64".
func TestInstallAcceptsEquivalentArchitectureSpelling(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	spelling := map[string]string{
		arch.Aarch64: "arm64",
		arch.X86_64:  "amd64",
		arch.Armv7h:  "armhf",
		arch.I686:    "i386",
	}[arch.Current()]
	if spelling == "" {
		t.Skipf("no alternative spelling defined for %s", arch.Current())
	}

	archive := buildPkgForArch(t, archives, "demo", "1.0", spelling)
	if _, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatalf("InstallPkg refused %q on %s: %v", spelling, arch.Current(), err)
	}
}

// A setuid binary that installs without its setuid bit is not a working binary:
// su, mount and ping are all present, executable, and broken. The bits were
// dropped twice -- once by extraction policy, once by Perm() at commit -- and
// the sticky bit on /tmp went the same way, which the image build had to paper
// over with an explicit chmod.
func TestInstallPreservesSetuidSetgidAndSticky(t *testing.T) {
	sysroot, archives, db := testEnv(t)

	archive := buildPkg(t, archives, "privs", "1.0", nil, []pkgFile{
		{path: "usr/bin/su", body: "#!/bin/sh\n", mode: 0o4755},
		{path: "usr/bin/wall", body: "#!/bin/sh\n", mode: 0o2755},
		{path: "tmp", isDir: true, mode: 0o1777},
		// The negative arm. Without a file that must come out UNPRIVILEGED,
		// this test passes just as happily against an install path that sets
		// setuid on everything it extracts -- a far worse defect than the one
		// the positive arm guards, and one that would look like success.
		{path: "usr/bin/plain", body: "#!/bin/sh\n", mode: 0o755},
		{path: "usr/share/doc", isDir: true, mode: 0o755},
	})

	if _, err := InstallPkg(archive, InstallOptions{Sysroot: sysroot}, db, nil); err != nil {
		t.Fatalf("InstallPkg: %v", err)
	}

	for _, tc := range []struct {
		path string
		want fs.FileMode
	}{
		{"usr/bin/su", fs.ModeSetuid},
		{"usr/bin/wall", fs.ModeSetgid},
		{"tmp", fs.ModeSticky},
	} {
		fi, err := os.Stat(filepath.Join(sysroot, tc.path))
		if err != nil {
			t.Fatalf("stat %s: %v", tc.path, err)
		}
		if fi.Mode()&tc.want == 0 {
			t.Errorf("%s: mode %v lost %v", tc.path, fi.Mode(), tc.want)
		}
	}

	// Installing must not GRANT privilege it was not given. Paired with the
	// loop above: one arm catches a lost bit, the other an invented one, and
	// neither is meaningful without the other.
	for _, path := range []string{"usr/bin/plain", "usr/share/doc"} {
		fi, err := os.Stat(filepath.Join(sysroot, path))
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if special := fi.Mode() & (fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky); special != 0 {
			t.Errorf("%s: mode %v gained %v; installing must not grant privilege", path, fi.Mode(), special)
		}
		if fi.Mode().Perm() != 0o755 {
			t.Errorf("%s: permissions %04o, want 0755", path, fi.Mode().Perm())
		}
	}

	// The recorded mode must agree with what is on disk, in the encoding the
	// rest of the world uses for a mode.
	files, err := db.Files("privs")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path == "usr/bin/su" && f.Mode != 0o4755 {
			t.Errorf("recorded mode for su = %#o, want %#o", f.Mode, 0o4755)
		}
		if f.Path == "usr/bin/plain" && f.Mode != 0o755 {
			t.Errorf("recorded mode for plain = %#o, want %#o", f.Mode, 0o755)
		}
	}
}
