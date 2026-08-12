package buildsteps

import (
	"os"
	"path/filepath"
	"tape/builder/utils"
	"tape/common/manifest"
	"testing"

	"github.com/spf13/viper"
)

// Stage9Wrap end to end: a recipe declaring libXau must produce a manifest
// declaring libXau.
//
// This is the whole builder half of the case bug. Before it, the recipe was
// read with viper (lower-casing the key on the way in) and the manifest written
// with viper (lower-casing it again on the way out), so the fix needed both
// ends -- reading correctly and writing through viper would have produced the
// same broken file.
func TestStage9WrapPreservesDependencyCase(t *testing.T) {
	pkgPath := t.TempDir()
	out := t.TempDir()

	recipe := `
[package]
name = "cairo"
description = "2D vector graphics library"
version = "1.18.4"
subversion = "3"
authors = ["The cairo project"]
packagers = ["ItzYanick"]

[dependencies]
libXau = ">=1.0"
libX11 = ">=1.8.10"
NetworkManager = ">=1.50"
glibc = ">=2.42.0"

[dependencies.build]
meson = ""
`
	if err := os.WriteFile(utils.PkgBuildConfigPath(pkgPath), []byte(recipe), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stage9Wrap copies the install tree; an empty one is enough here, the
	// payload is not what is under test.
	if err := os.MkdirAll(utils.DirWorkInstall(pkgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, err := utils.PkgBuildConfigLoad(pkgPath)
	if err != nil {
		t.Fatal(err)
	}
	SetSettings(pkgPath, "aarch64", pkgPath, cfg, out)

	if err := Stage9Wrap(); err != nil {
		t.Fatalf("Stage9Wrap: %v", err)
	}

	manifestPath := filepath.Join(utils.DirWrap(pkgPath), "TAPEPACKAGE.toml")
	deps, err := manifest.ReadDependencies(manifestPath)
	if err != nil {
		t.Fatalf("reading the manifest it wrote: %v", err)
	}

	want := map[string]string{
		"libXau": ">=1.0", "libX11": ">=1.8.10",
		"NetworkManager": ">=1.50", "glibc": ">=2.42.0",
	}
	if len(deps.Runtime) != len(want) {
		t.Fatalf("manifest declares %v, want %v", deps.Runtime, want)
	}
	for name, constraint := range want {
		got, ok := deps.Runtime[name]
		if !ok {
			body, _ := os.ReadFile(manifestPath)
			t.Errorf("manifest does not declare %q; it says:\n%s", name, body)
			continue
		}
		if got != constraint {
			t.Errorf("%s = %q, want %q", name, got, constraint)
		}
	}

	// Build dependencies must not reach the package: meson is needed to build
	// cairo and is not needed to run it.
	if _, ok := deps.Runtime["meson"]; ok {
		t.Errorf("build dependency meson leaked into the package manifest: %v", deps.Runtime)
	}

	// The fields the daemon and tape-repo read through viper must survive the
	// change of writer, or every published package's identity changes shape.
	v := viper.New()
	v.SetConfigFile(manifestPath)
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("viper cannot read the manifest Stage9Wrap wrote: %v", err)
	}
	for key, expected := range map[string]string{
		"package.name": "cairo", "package.version": "1.18.4",
		"package.subversion": "3", "package.arch": "aarch64",
		"package.description": "2D vector graphics library",
	} {
		if got := v.GetString(key); got != expected {
			t.Errorf("%s = %q, want %q", key, got, expected)
		}
	}
	if authors := v.GetStringSlice("package.authors"); len(authors) != 1 || authors[0] != "The cairo project" {
		t.Errorf("package.authors = %v, want [The cairo project]", authors)
	}
	if packagers := v.GetStringSlice("package.packagers"); len(packagers) != 1 || packagers[0] != "ItzYanick" {
		t.Errorf("package.packagers = %v, want [ItzYanick]", packagers)
	}

	// And the archive is there, named the way the repository will serve it.
	if _, err := os.Stat(filepath.Join(out, "cairo-1.18.4-3.aarch64.tape.tar.gz")); err != nil {
		t.Errorf("expected archive: %v", err)
	}
}

// The control: the pre-fix path, on the same recipe, in the same test binary.
//
// Reading with GetStringMap and writing with viper is exactly what Stage9Wrap
// used to do. It must still produce the broken manifest -- if viper ever stops
// folding keys, this fails and the comments explaining the whole fix are wrong
// and need rewriting rather than quietly outliving their reason.
func TestControlViperRoundTripStillBreaksTheSameRecipe(t *testing.T) {
	pkgPath := t.TempDir()

	recipe := "[package]\nname = \"cairo\"\n\n[dependencies]\nlibXau = \">=1.0\"\n"
	if err := os.WriteFile(utils.PkgBuildConfigPath(pkgPath), []byte(recipe), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := utils.PkgBuildConfigLoad(pkgPath)
	if err != nil {
		t.Fatal(err)
	}

	// Read the old way.
	deps := cfg.GetStringMap("dependencies")
	if _, ok := deps["libXau"]; ok {
		t.Fatalf("viper no longer lower-cases keys on read: %v", deps)
	}

	// Write the old way, from a map whose keys ARE correctly cased -- proving
	// the read fix alone would not have been enough.
	outPath := filepath.Join(pkgPath, "TAPEPACKAGE.toml")
	w := viper.New()
	w.SetConfigFile(outPath)
	w.Set("package.name", "cairo")
	w.Set("dependencies", map[string]any{"libXau": ">=1.0"})
	if err := w.WriteConfig(); err != nil {
		t.Fatal(err)
	}

	written, err := manifest.ReadDependencies(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := written.Runtime["libXau"]; ok {
		t.Fatalf("viper no longer lower-cases keys on write; the write half of this "+
			"fix has lost its reason and the comments must be corrected (got %v)", written.Runtime)
	}
	if _, ok := written.Runtime["libxau"]; !ok {
		t.Fatalf("viper wrote neither libXau nor libxau: %v", written.Runtime)
	}
}
