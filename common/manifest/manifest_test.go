package manifest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const recipe = `
[package]
name = "cairo"
version = "1.18.0"
subversion = "1"

[dependencies]
libXau = ">=1.0"
libX11 = ""
NetworkManager = ">=1.50"
glibc = ""

[dependencies.build]
libXfixes = ""
meson = ""
`

// The read half. libXau must arrive as libXau.
func TestReadDependenciesKeepsCase(t *testing.T) {
	p := write(t, t.TempDir(), "TAPEBUILD.toml", recipe)

	deps, err := ReadDependencies(p)
	if err != nil {
		t.Fatalf("ReadDependencies: %v", err)
	}

	want := map[string]string{
		"libXau": ">=1.0", "libX11": "", "NetworkManager": ">=1.50", "glibc": "",
	}
	if len(deps.Runtime) != len(want) {
		t.Fatalf("Runtime = %v, want %v", deps.Runtime, want)
	}
	for name, constraint := range want {
		got, ok := deps.Runtime[name]
		if !ok {
			t.Errorf("dependency %q missing (got %v)", name, deps.Runtime)
			continue
		}
		if got != constraint {
			t.Errorf("dependency %q constraint = %q, want %q", name, got, constraint)
		}
	}

	// The build sub-table is separated out, not left in Runtime as a package
	// called "build", and its own names keep their case too.
	if _, ok := deps.Runtime[BuildDepsKey]; ok {
		t.Errorf("%q leaked into Runtime", BuildDepsKey)
	}
	if _, ok := deps.Build["libXfixes"]; !ok {
		t.Errorf("build dependency libXfixes missing (got %v)", deps.Build)
	}
}

// The control for the read half: viper, on the same bytes, mangles them.
//
// Without this the test above passes just as well against a TOML library that
// happens to fold nothing today, and says nothing about the defect it was
// written for. This runs the exact call the builder used to make and requires
// it to produce the broken names -- so the fixture is known to be one viper
// gets wrong, rather than assumed to be.
func TestControlViperLowercasesTheSameFile(t *testing.T) {
	p := write(t, t.TempDir(), "TAPEBUILD.toml", recipe)

	v := viper.New()
	v.SetConfigFile(p)
	if err := v.ReadInConfig(); err != nil {
		t.Fatal(err)
	}

	got := v.GetStringMap("dependencies")
	if _, ok := got["libXau"]; ok {
		t.Fatalf("viper returned libXau with its case intact; this fixture no longer "+
			"exercises the bug, so the test above is not evidence (got %v)", got)
	}
	if _, ok := got["libxau"]; !ok {
		t.Fatalf("viper returned neither libXau nor libxau: %v", got)
	}

	// And the value it reads for a plain field is untouched, which is the whole
	// asymmetry: package.name is a VALUE and survived, so nothing about a
	// published package looked wrong except the names it asked for.
	if name := v.GetString("package.name"); name != "cairo" {
		t.Errorf("package.name = %q, want cairo", name)
	}
}

// The write half. viper's writer lower-cases map keys on the way out as well,
// so a correctly-cased map handed to WriteConfig still produced a wrong file --
// reading the recipe properly and writing it back through viper would have
// fixed nothing.
func TestWritePackageKeepsCase(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "TAPEPACKAGE.toml")

	if err := WritePackage(p, PackageManifest{
		Package:      Package{Name: "cairo", Version: "1.18.0", Subversion: "1", Arch: "aarch64"},
		Dependencies: map[string]string{"libXau": ">=1.0", "glibc": ""},
	}); err != nil {
		t.Fatalf("WritePackage: %v", err)
	}

	// Read it back the way the daemon and tape-repo now do.
	deps, err := ReadDependencies(p)
	if err != nil {
		t.Fatalf("ReadDependencies: %v", err)
	}
	if _, ok := deps.Runtime["libXau"]; !ok {
		t.Fatalf("libXau did not survive the round trip: %v", deps.Runtime)
	}

	// Build dependencies must not appear in a built package's manifest at all.
	if len(deps.Build) != 0 {
		t.Errorf("built manifest carries build dependencies: %v", deps.Build)
	}

	// And the fields the rest of tape reads through viper must still be there,
	// spelled the same way, or this writer has quietly changed the format.
	v := viper.New()
	v.SetConfigFile(p)
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("viper cannot read the manifest this writer produced: %v", err)
	}
	for key, want := range map[string]string{
		"package.name": "cairo", "package.version": "1.18.0",
		"package.subversion": "1", "package.arch": "aarch64",
	} {
		if got := v.GetString(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// A missing table is ordinary, not an error: plenty of packages depend on
// nothing.
func TestReadDependenciesWithNoTable(t *testing.T) {
	p := write(t, t.TempDir(), "TAPEPACKAGE.toml", "[package]\nname = 'glibc'\n")

	deps, err := ReadDependencies(p)
	if err != nil {
		t.Fatalf("ReadDependencies: %v", err)
	}
	if len(deps.Runtime) != 0 || len(deps.Build) != 0 {
		t.Errorf("expected no dependencies, got %v / %v", deps.Runtime, deps.Build)
	}
}

// A "build" key that is not a table is reported rather than skipped. The old
// builder set it to nil, which viper omitted from the file entirely -- see the
// real published manifest below -- so nothing legitimate hits this path, and
// anything that does is malformed.
func TestReadDependenciesRejectsNonTableBuildKey(t *testing.T) {
	p := write(t, t.TempDir(), "TAPEPACKAGE.toml",
		"[package]\nname = 'cairo'\n\n[dependencies]\nglibc = ''\nbuild = []\n")

	if _, err := ReadDependencies(p); err == nil {
		t.Fatal("expected an error for a [dependencies] build key that is not a table")
	}
}

// A real manifest, copied byte for byte out of cairo-1.18.4-2.aarch64 as
// published. It is the bug's own artefact: the package is called libX11 and
// this file, written by the old builder, asks for "libx11".
//
// Every already-published manifest looks like this, so the reader must handle
// it unchanged -- the resolver's folding is what makes these names resolve, and
// nothing here rewrites them.
const publishedCairoManifest = `[dependencies]
fontconfig = '>=2.16.0'
freetype = '>=2.13.3'
glib = '>=2.84.0'
glibc = '>=2.42.0'
libpng = '>=1.6.47'
libx11 = '>=1.8.10'
libxext = '>=1.3.6'
libxrender = '>=0.9.11'
pixman = '>=0.44.2'
zlib = '>=1.3.1'

[package]
arch = 'aarch64'
authors = ['The cairo project']
description = '2D vector graphics library'
name = 'cairo'
packagers = ['ItzYanick']
subversion = '2'
version = '1.18.4'
`

func TestReadPublishedManifest(t *testing.T) {
	p := write(t, t.TempDir(), "TAPEPACKAGE.toml", publishedCairoManifest)

	deps, err := ReadDependencies(p)
	if err != nil {
		t.Fatalf("ReadDependencies on a real published manifest: %v", err)
	}
	if len(deps.Runtime) != 10 {
		t.Errorf("read %d dependencies, want 10: %v", len(deps.Runtime), deps.Runtime)
	}
	// Read back as written. Repairing the case here would be guesswork -- the
	// index is the authority on how a package is spelled, and the resolver is
	// where the two are reconciled.
	if got, ok := deps.Runtime["libx11"]; !ok || got != ">=1.8.10" {
		t.Errorf("libx11 = %q (present: %v), want >=1.8.10 exactly as published", got, ok)
	}
	if len(deps.Build) != 0 {
		t.Errorf("published manifest carries build dependencies: %v", deps.Build)
	}
}

// A malformed entry is reported rather than skipped: a dependency dropped in
// silence is a package that installs and cannot run.
func TestReadDependenciesRejectsNonStringConstraint(t *testing.T) {
	p := write(t, t.TempDir(), "TAPEBUILD.toml",
		"[package]\nname = 'cairo'\n\n[dependencies]\nglibc = 1\n")

	if _, err := ReadDependencies(p); err == nil {
		t.Fatal("expected an error for a non-string version constraint")
	}
}
