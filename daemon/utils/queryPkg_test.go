package utils

import (
	"os"
	"path/filepath"
	"tape/common/arch"
	"tape/common/database"
	"testing"
)

func TestCombineConstraints(t *testing.T) {
	for _, tc := range []struct{ a, b, want string }{
		{"", ">=2.0", ">=2.0"},
		{">=2.0", "", ">=2.0"},
		{">=2.0", ">=2.0", ">=2.0"},
		{">=2.0", ">=3.0", ">=2.0, >=3.0"},
	} {
		if got := combineConstraints(tc.a, tc.b); got != tc.want {
			t.Errorf("combineConstraints(%q,%q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSatisfies(t *testing.T) {
	for _, tc := range []struct {
		version, constraint string
		want                bool
	}{
		{"2.0.0", "", true},
		{"2.0.0", ">=2.0", true},
		{"2.0.0", ">=3.0", false},
		{"3.1.0", ">=2.0, >=3.0", true},
		// Unparseable input must not be treated as satisfied, or a bad version
		// string silently short-circuits the check it exists to perform.
		{"alpha", ">=2.0", false},
		{"2.0.0", "not-a-constraint", false},
	} {
		if got := satisfies(tc.version, tc.constraint); got != tc.want {
			t.Errorf("satisfies(%q,%q) = %v, want %v", tc.version, tc.constraint, got, tc.want)
		}
	}
}

// resolverRepo builds a repository index on disk and points the config at it.
type repoPkg struct {
	name, version string
	deps          map[string]string
}

func resolverRepo(t *testing.T, pkgs []repoPkg) {
	t.Helper()

	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	if err := os.MkdirAll(filepath.Join(cache, "repos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "repos"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAPE_CONFIG_DIR", dir)
	t.Setenv("TAPE_CACHE_DIR", cache)

	cfg := "[repo]\nname = 'testrepo'\nbaseurl = 'https://example.invalid'\nenabled = true\n"
	if err := os.WriteFile(filepath.Join(dir, "repos", "testrepo.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := database.RepoOpenByPath(filepath.Join(cache, "repos", "testrepo.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.RepoModelPkgs{}, &database.RepoModelDependencies{}); err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		row := database.RepoModelPkgs{
			Name: p.name, Version: p.version, Subversion: "1", Arch: arch.Current(),
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		for dn, dc := range p.deps {
			if err := db.Create(&database.RepoModelDependencies{
				PkgId: row.ID, Name: dn, VersionConstraint: dc,
			}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
}

// The diamond, with the constraint shape that actually breaks: an upper bound.
//
// A lower bound alone hides the bug, because selectLatest already picks the
// newest matching version -- so a later ">=3.0" is satisfied by accident. Only
// an upper bound exposes it: c is pinned at 2.0 by one branch and the other
// branch's ">=3.0" is dropped in silence, yielding an install that cannot work
// and no indication that anything was ignored.
//
// The two requirements here cannot both be met, so the honest answer is to
// report c as unresolved rather than to pick one and say nothing.
func TestDiamondWithUnsatisfiableConstraintsDoesNotSilentlyPick(t *testing.T) {
	resolverRepo(t, []repoPkg{
		{name: "app", version: "1.0.0", deps: map[string]string{"left": ">=1.0", "right": ">=1.0"}},
		{name: "left", version: "1.0.0", deps: map[string]string{"c": "<3.0"}},
		{name: "right", version: "1.0.0", deps: map[string]string{"c": ">=3.0"}},
		{name: "c", version: "2.0.0"},
		{name: "c", version: "3.0.0"},
	})

	// Map iteration order decides which branch is walked first; the answer must
	// not depend on it, so every run is checked.
	for i := 0; i < 8; i++ {
		_, deps, err := QueryPkg("app", true, "")
		if err != nil {
			continue // an explicit failure is an acceptable answer
		}
		for _, d := range deps {
			if d["name"] != "c" {
				continue
			}
			if d["error"] == "" {
				t.Fatalf("run %d: c resolved to %s despite <3.0 and >=3.0 both being required; "+
					"one constraint was dropped silently", i, d["version"])
			}
		}
	}
}

// Satisfiable, but only if both constraints are considered: >=1.0 alone selects
// 3.0.0, which violates the other branch's <3.0. The answer is 2.0.0.
func TestDiamondNarrowsToVersionSatisfyingBoth(t *testing.T) {
	resolverRepo(t, []repoPkg{
		{name: "app", version: "1.0.0", deps: map[string]string{"left": ">=1.0", "right": ">=1.0"}},
		{name: "left", version: "1.0.0", deps: map[string]string{"c": ">=1.0"}},
		{name: "right", version: "1.0.0", deps: map[string]string{"c": "<3.0"}},
		{name: "c", version: "1.0.0"},
		{name: "c", version: "2.0.0"},
		{name: "c", version: "3.0.0"},
	})

	for i := 0; i < 8; i++ {
		_, deps, err := QueryPkg("app", true, "")
		if err != nil {
			t.Fatalf("run %d: QueryPkg: %v", i, err)
		}
		for _, d := range deps {
			if d["name"] == "c" && d["version"] != "2.0.0" {
				t.Fatalf("run %d: c resolved to %q, want 2.0.0 (>=1.0 and <3.0)", i, d["version"])
			}
		}
	}
}

// A cycle must still terminate now that revisits can re-resolve.
func TestDependencyCycleTerminates(t *testing.T) {
	resolverRepo(t, []repoPkg{
		{name: "a", version: "1.0.0", deps: map[string]string{"b": ">=1.0"}},
		{name: "b", version: "1.0.0", deps: map[string]string{"a": ">=1.0"}},
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, _, err := QueryPkg("a", true, ""); err != nil {
			t.Errorf("QueryPkg: %v", err)
		}
	}()
	<-done
}
