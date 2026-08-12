package utils

import (
	"os"
	"path/filepath"
	"tape/common/arch"
	"tape/common/database"
	"tape/common/utils"
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

// The case bug, end to end.
//
// Every dependency name in every manifest published before the builder was
// fixed is lower-cased, because tape-builder read [dependencies] through viper
// and viper lower-cases every key it returns. The package those names point at
// keeps its own case, because `package.name` is a VALUE. So the index holds a
// row called "libXau" and sixteen manifests asking for "libxau", and
// `tape install gtk4` stopped at "libxau: package not found".
//
// The fixture is that exact shape: the dependant asks in lower case, the
// package exists in mixed case, and nothing else differs.
func TestLowercasedDependencyNameResolvesToMixedCasePackage(t *testing.T) {
	resolverRepo(t, []repoPkg{
		{name: "cairo", version: "1.18.0", deps: map[string]string{"libxau": "", "libxrender": ""}},
		{name: "libXau", version: "1.0.11"},
		{name: "libXrender", version: "0.9.11"},
	})

	pkg, deps, err := QueryPkg("cairo", true, "")
	if err != nil {
		t.Fatalf("QueryPkg: %v", err)
	}
	if pkg["error"] != "" {
		t.Fatalf("cairo: %s", pkg["error"])
	}

	got := make(map[string]string, len(deps))
	for _, d := range deps {
		if d["error"] != "" {
			t.Fatalf("dependency %s: %s", d["name"], d["error"])
		}
		got[d["name"]] = d["version"]
	}

	// The name reported is the one the INDEX spells, not the one the manifest
	// asked for. That is what the archive on the server is called, so a
	// lower-cased answer here would resolve and then 404 on download.
	for name, version := range map[string]string{"libXau": "1.0.11", "libXrender": "0.9.11"} {
		if got[name] != version {
			t.Errorf("dependency %s: resolved to %q, want %q (resolved set: %v)", name, got[name], version, got)
		}
	}
	if len(got) != 2 {
		t.Errorf("resolved %d dependencies, want 2: %v", len(got), got)
	}
}

// Control, first arm: the pre-fix predicate against the SAME fixture.
//
// Without this the test above proves nothing -- it would pass identically if
// sqlite happened to compare names case-insensitively by default, or if the
// fixture had quietly stored a lower-case row. This runs the exact query the
// resolver ran before the fix and requires it to find nothing, so the fixture
// is known to exercise case-sensitivity rather than to be assumed to.
func TestControlOldCaseSensitiveMatchFindsNothing(t *testing.T) {
	resolverRepo(t, []repoPkg{{name: "libXau", version: "1.0.11"}})

	db, err := database.RepoOpenByName("testrepo")
	if err != nil {
		t.Fatalf("RepoOpenByName: %v", err)
	}

	var rows []database.RepoModelPkgs
	if tx := db.Find(&rows, "name = ?", "libxau"); tx.Error != nil {
		t.Fatalf("query: %v", tx.Error)
	}
	if len(rows) != 0 {
		t.Fatalf("the pre-fix predicate matched %d row(s) for %q; this fixture does not "+
			"exercise the bug, so the positive test above is not evidence", len(rows), "libxau")
	}

	// And the same predicate with the fix's collation must find it, or the two
	// halves of this control are testing different things.
	if tx := db.Find(&rows, nameMatch, "libxau"); tx.Error != nil {
		t.Fatalf("query: %v", tx.Error)
	}
	if len(rows) != 1 || rows[0].Name != "libXau" {
		t.Fatalf("NOCASE predicate returned %d row(s) %v, want exactly libXau", len(rows), rows)
	}
}

// Control, second arm: the resolver must still be able to say no.
//
// A resolver that matched everything would pass every test above. The message
// is what is checked, not the presence of an error: "package not found" is the
// string that named the symptom, and a different failure reaching this point
// reads identically to a working lookup unless the text is compared.
func TestControlAbsentPackageStillReportsNotFound(t *testing.T) {
	resolverRepo(t, []repoPkg{{name: "libXau", version: "1.0.11"}})

	pkg, _, err := QueryPkg("libXauXX", true, "")
	if err != nil {
		t.Fatalf("QueryPkg: %v", err)
	}
	if pkg["error"] != "package not found" {
		t.Fatalf("absent package reported %q, want %q -- case-insensitive matching "+
			"must not turn a missing package into a hit", pkg["error"], "package not found")
	}
}

// Two spellings of one name must not become two identities.
//
// The traversal's visited set and the caller's dedupe are both keyed by name;
// keyed literally, a package reached once as "libx11" and once as "libX11" is
// resolved twice, installed twice, and each copy is blind to the other's
// version constraint -- which is the silent constraint-drop the visited set was
// added to prevent, reintroduced through the spelling.
func TestTwoSpellingsResolveToOnePackage(t *testing.T) {
	resolverRepo(t, []repoPkg{
		{name: "app", version: "1.0.0", deps: map[string]string{"left": "", "right": ""}},
		{name: "left", version: "1.0.0", deps: map[string]string{"libx11": ""}},
		{name: "right", version: "1.0.0", deps: map[string]string{"libX11": ""}},
		{name: "libX11", version: "1.8.10"},
	})

	// Map iteration order decides which spelling is walked first; neither may
	// change the answer.
	for i := 0; i < 8; i++ {
		_, deps, err := QueryPkg("app", true, "")
		if err != nil {
			t.Fatalf("run %d: QueryPkg: %v", i, err)
		}
		count := 0
		for _, d := range deps {
			if d["error"] != "" {
				t.Fatalf("run %d: %s: %s", i, d["name"], d["error"])
			}
			if utils.FoldName(d["name"]) == "libx11" {
				count++
				if d["name"] != "libX11" {
					t.Errorf("run %d: reported as %q, want the index spelling libX11", i, d["name"])
				}
			}
		}
		if count != 1 {
			t.Fatalf("run %d: libX11 appears %d times in the resolved set, want 1", i, count)
		}
	}
}

// A constraint carried on the lower-cased spelling must still bind the
// mixed-case package: folding decides what matches, and must not quietly drop
// the requirement attached to the name it folded.
func TestConstraintOnLowercasedNameStillApplies(t *testing.T) {
	resolverRepo(t, []repoPkg{
		{name: "app", version: "1.0.0", deps: map[string]string{"libX11": ">=1.0", "helper": ""}},
		{name: "helper", version: "1.0.0", deps: map[string]string{"libx11": "<1.8"}},
		{name: "libX11", version: "1.7.0"},
		{name: "libX11", version: "1.8.10"},
	})

	for i := 0; i < 8; i++ {
		_, deps, err := QueryPkg("app", true, "")
		if err != nil {
			t.Fatalf("run %d: QueryPkg: %v", i, err)
		}
		for _, d := range deps {
			if utils.FoldName(d["name"]) != "libx11" {
				continue
			}
			if d["version"] != "1.7.0" {
				t.Fatalf("run %d: libX11 resolved to %q, want 1.7.0 -- the \"<1.8\" written "+
					"against the lower-cased spelling was dropped", i, d["version"])
			}
		}
	}
}
