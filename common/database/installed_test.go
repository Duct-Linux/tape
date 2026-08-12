package database

import (
	"errors"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *InstalledDB {
	t.Helper()
	db, err := OpenInstalledDB(filepath.Join(t.TempDir(), "installed.db"))
	if err != nil {
		t.Fatalf("OpenInstalledDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func record(t *testing.T, db *InstalledDB, name string, reason InstallReason, paths []string, deps ...string) {
	t.Helper()

	files := make([]InstalledFile, 0, len(paths))
	for _, p := range paths {
		files = append(files, InstalledFile{Path: p, Sha256: "deadbeef", Mode: 0644})
	}

	edges := make([]InstalledDep, 0, len(deps))
	for _, d := range deps {
		edges = append(edges, InstalledDep{Name: d})
	}

	pkg := InstalledPkg{Name: name, Version: "1.0", Subversion: "1", Arch: "x86_64", Repo: "core", Reason: reason}
	if err := db.Record(pkg, files, edges); err != nil {
		t.Fatalf("Record(%s): %v", name, err)
	}
}

func TestRecordAndGet(t *testing.T) {
	db := openTestDB(t)
	record(t, db, "bash", ReasonExplicit, []string{"usr/bin/bash"})

	pkg, err := db.Get("bash")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if pkg.Name != "bash" || pkg.Version != "1.0" || pkg.Reason != ReasonExplicit {
		t.Errorf("unexpected package %+v", pkg)
	}
	if pkg.InstalledAt.IsZero() {
		t.Error("InstalledAt was not set")
	}
}

func TestGetMissingReturnsErrNotInstalled(t *testing.T) {
	db := openTestDB(t)

	_, err := db.Get("nope")
	if !errors.Is(err, ErrNotInstalled) {
		t.Errorf("Get(missing) = %v, want ErrNotInstalled", err)
	}

	installed, err := db.IsInstalled("nope")
	if err != nil {
		t.Fatalf("IsInstalled: %v", err)
	}
	if installed {
		t.Error("IsInstalled(missing) = true")
	}
}

func TestFilesAreDeepestFirst(t *testing.T) {
	db := openTestDB(t)
	record(t, db, "demo", ReasonExplicit, []string{
		"usr/bin/demo",
		"usr/share/demo/data/deep/file.txt",
		"usr/share/demo/readme",
	})

	files, err := db.Files("demo")
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("got %d files, want 3", len(files))
	}
	// Deepest first, so removal can unlink then prune parents in order.
	for i := 1; i < len(files); i++ {
		if len(files[i-1].Path) < len(files[i].Path) {
			t.Errorf("files not ordered deepest-first: %q before %q", files[i-1].Path, files[i].Path)
		}
	}
}

func TestFileOwner(t *testing.T) {
	db := openTestDB(t)
	record(t, db, "bash", ReasonExplicit, []string{"usr/bin/bash"})

	owner, err := db.FileOwner("usr/bin/bash")
	if err != nil {
		t.Fatalf("FileOwner: %v", err)
	}
	if owner != "bash" {
		t.Errorf("FileOwner = %q, want %q", owner, "bash")
	}

	if _, err := db.FileOwner("usr/bin/unowned"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("FileOwner(unowned) = %v, want ErrNotInstalled", err)
	}
}

// Two packages must not be able to claim the same path -- this is what stops an
// install from silently clobbering another package's files.
func TestCheckConflictsDetectsOverlap(t *testing.T) {
	db := openTestDB(t)
	record(t, db, "bash", ReasonExplicit, []string{"usr/bin/bash", "usr/share/man/bash.1"})

	conflicts, err := db.CheckConflicts([]string{"usr/bin/bash", "usr/bin/fresh"}, "")
	if err != nil {
		t.Fatalf("CheckConflicts: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1: %+v", len(conflicts), conflicts)
	}
	if conflicts[0].Path != "usr/bin/bash" || conflicts[0].Owner != "bash" {
		t.Errorf("unexpected conflict %+v", conflicts[0])
	}
}

// Reinstalling or upgrading a package must not conflict with its own files.
func TestCheckConflictsExcludesSelf(t *testing.T) {
	db := openTestDB(t)
	record(t, db, "bash", ReasonExplicit, []string{"usr/bin/bash"})

	conflicts, err := db.CheckConflicts([]string{"usr/bin/bash"}, "bash")
	if err != nil {
		t.Fatalf("CheckConflicts: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("got %d conflicts against self, want 0: %+v", len(conflicts), conflicts)
	}
}

func TestCheckConflictsHandlesLargeManifests(t *testing.T) {
	db := openTestDB(t)

	paths := make([]string, 0, 1200)
	for i := 0; i < 1200; i++ {
		paths = append(paths, filepath.Join("usr/share/big", string(rune('a'+i%26)), itoa(i)))
	}
	record(t, db, "big", ReasonExplicit, paths)

	// Chunking must not drop any of them.
	conflicts, err := db.CheckConflicts(paths, "")
	if err != nil {
		t.Fatalf("CheckConflicts: %v", err)
	}
	if len(conflicts) != len(paths) {
		t.Errorf("got %d conflicts, want %d", len(conflicts), len(paths))
	}
}

// Re-recording must replace the manifest, not accumulate stale rows.
func TestRecordReplacesPreviousManifest(t *testing.T) {
	db := openTestDB(t)
	record(t, db, "demo", ReasonExplicit, []string{"usr/bin/old", "usr/share/old.txt"})
	record(t, db, "demo", ReasonExplicit, []string{"usr/bin/new"})

	files, err := db.Files("demo")
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 1 || files[0].Path != "usr/bin/new" {
		t.Errorf("manifest not replaced: %+v", files)
	}

	// The old paths must be unowned now, or a later install would see phantom
	// conflicts against a package that no longer ships them.
	if _, err := db.FileOwner("usr/bin/old"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("stale manifest row survived: %v", err)
	}
}

func TestRemove(t *testing.T) {
	db := openTestDB(t)
	record(t, db, "demo", ReasonExplicit, []string{"usr/bin/demo"}, "libdemo")

	if err := db.Remove("demo"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := db.Get("demo"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("package still present after Remove: %v", err)
	}
	if _, err := db.FileOwner("usr/bin/demo"); !errors.Is(err, ErrNotInstalled) {
		t.Error("manifest rows survived Remove")
	}
	if deps, _ := db.RequiredBy("libdemo"); len(deps) != 0 {
		t.Errorf("dependency edges survived Remove: %v", deps)
	}
}

func TestRemoveMissing(t *testing.T) {
	db := openTestDB(t)
	if err := db.Remove("nope"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("Remove(missing) = %v, want ErrNotInstalled", err)
	}
}

func TestRequiredBy(t *testing.T) {
	db := openTestDB(t)
	record(t, db, "libssl", ReasonDependency, []string{"usr/lib/libssl.so"})
	record(t, db, "curl", ReasonExplicit, []string{"usr/bin/curl"}, "libssl")
	record(t, db, "wget", ReasonExplicit, []string{"usr/bin/wget"}, "libssl")

	dependents, err := db.RequiredBy("libssl")
	if err != nil {
		t.Fatalf("RequiredBy: %v", err)
	}
	if len(dependents) != 2 {
		t.Errorf("RequiredBy(libssl) = %v, want 2 entries", dependents)
	}
}

func TestOrphans(t *testing.T) {
	db := openTestDB(t)
	record(t, db, "libssl", ReasonDependency, []string{"usr/lib/libssl.so"})
	record(t, db, "libz", ReasonDependency, []string{"usr/lib/libz.so"})
	record(t, db, "curl", ReasonExplicit, []string{"usr/bin/curl"}, "libssl")

	orphans, err := db.Orphans()
	if err != nil {
		t.Fatalf("Orphans: %v", err)
	}
	// libz is a dependency nothing needs; libssl is still required by curl;
	// curl is explicit and never an orphan.
	if len(orphans) != 1 || orphans[0] != "libz" {
		t.Errorf("Orphans = %v, want [libz]", orphans)
	}

	// Removing curl should orphan libssl too.
	if err := db.Remove("curl"); err != nil {
		t.Fatal(err)
	}
	orphans, err = db.Orphans()
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 2 {
		t.Errorf("Orphans after removing curl = %v, want 2 entries", orphans)
	}
}

func TestList(t *testing.T) {
	db := openTestDB(t)
	record(t, db, "zsh", ReasonExplicit, []string{"usr/bin/zsh"})
	record(t, db, "bash", ReasonExplicit, []string{"usr/bin/bash"})

	pkgs, err := db.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("got %d packages, want 2", len(pkgs))
	}
	if pkgs[0].Name != "bash" || pkgs[1].Name != "zsh" {
		t.Errorf("List not ordered by name: %v, %v", pkgs[0].Name, pkgs[1].Name)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf []byte
	for i > 0 {
		buf = append([]byte{byte('0' + i%10)}, buf...)
		i /= 10
	}
	return string(buf)
}

// The two sides of a dependency edge are written by code paths that disagree
// about case: a package's own Name comes from `package.name` in its manifest
// and keeps its case, while an edge's Name came from a [dependencies] key,
// which the old builder lower-cased. Every mixed-case package therefore looked
// like nothing depended on it.
func TestRequiredByMatchesRegardlessOfCase(t *testing.T) {
	db := openTestDB(t)
	record(t, db, "libX11", ReasonDependency, []string{"usr/lib/libX11.so"})
	// cairo's manifest asks for "libx11": what the old builder published.
	record(t, db, "cairo", ReasonExplicit, []string{"usr/lib/libcairo.so"}, "libx11")

	dependents, err := db.RequiredBy("libX11")
	if err != nil {
		t.Fatalf("RequiredBy: %v", err)
	}
	if len(dependents) != 1 || dependents[0] != "cairo" {
		t.Fatalf("RequiredBy(libX11) = %v, want [cairo] -- the lower-cased edge cairo "+
			"actually published was not matched", dependents)
	}
}

// The consequence of the above, and the dangerous one: a package half the
// desktop links against offered for deletion as an orphan.
func TestMixedCasePackageIsNotAnOrphan(t *testing.T) {
	db := openTestDB(t)
	record(t, db, "libX11", ReasonDependency, []string{"usr/lib/libX11.so"})
	record(t, db, "cairo", ReasonExplicit, []string{"usr/lib/libcairo.so"}, "libx11")

	orphans, err := db.Orphans()
	if err != nil {
		t.Fatalf("Orphans: %v", err)
	}
	for _, o := range orphans {
		if o == "libX11" {
			t.Fatalf("libX11 reported as an orphan while cairo depends on it (orphans: %v)", orphans)
		}
	}
}

// Control: Orphans must still find a real one. Case-insensitive matching that
// made every package look required would pass the test above and silently
// disable orphan detection altogether.
func TestControlOrphansStillFindsARealOrphan(t *testing.T) {
	db := openTestDB(t)
	record(t, db, "libX11", ReasonDependency, []string{"usr/lib/libX11.so"})
	record(t, db, "libXau", ReasonDependency, []string{"usr/lib/libXau.so"})
	record(t, db, "cairo", ReasonExplicit, []string{"usr/lib/libcairo.so"}, "libx11")

	orphans, err := db.Orphans()
	if err != nil {
		t.Fatalf("Orphans: %v", err)
	}
	if len(orphans) != 1 || orphans[0] != "libXau" {
		t.Fatalf("Orphans() = %v, want [libXau] -- nothing depends on libXau", orphans)
	}
}

// Get and Remove take a name a user typed, which need not match the case the
// manifest recorded.
func TestGetAndRemoveAreCaseInsensitive(t *testing.T) {
	db := openTestDB(t)
	record(t, db, "libXau", ReasonExplicit, []string{"usr/lib/libXau.so"})

	pkg, err := db.Get("libxau")
	if err != nil {
		t.Fatalf("Get(libxau): %v", err)
	}
	// The record keeps its own spelling; only the lookup folds.
	if pkg.Name != "libXau" {
		t.Errorf("Get(libxau).Name = %q, want libXau", pkg.Name)
	}

	if err := db.Remove("LIBXAU"); err != nil {
		t.Fatalf("Remove(LIBXAU): %v", err)
	}
	if _, err := db.Get("libXau"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("after Remove, Get returned %v, want ErrNotInstalled", err)
	}
}

// Control: a package that is genuinely absent must still be absent. A fold
// broad enough to match anything would satisfy every test above.
func TestControlAbsentPackageStillNotInstalled(t *testing.T) {
	db := openTestDB(t)
	record(t, db, "libXau", ReasonExplicit, []string{"usr/lib/libXau.so"})

	if _, err := db.Get("libXauXX"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Get(libXauXX) returned %v, want ErrNotInstalled", err)
	}
}
