package utils

import (
	"path/filepath"
	"testing"
)

func TestResolvePathKeepsAbsolutePaths(t *testing.T) {
	pwd := "/Users/yanick/Projects/Duct/tape"

	// The regression: an absolute -o used to be joined onto pwd, producing
	// /Users/yanick/Projects/Duct/tape/tmp/out and a silently misplaced package.
	for _, abs := range []string{"/tmp/out", "/var/cache/tape", "/tmp/out/"} {
		got := ResolvePath(pwd, abs)
		if !filepath.IsAbs(got) {
			t.Errorf("ResolvePath(%q, %q) = %q, want an absolute path", pwd, abs, got)
		}
		if got != filepath.Clean(abs) {
			t.Errorf("ResolvePath(%q, %q) = %q, want %q", pwd, abs, got, filepath.Clean(abs))
		}
	}
}

func TestResolvePathJoinsRelativePaths(t *testing.T) {
	pwd := "/Users/yanick/Projects/Duct/tape"

	cases := map[string]string{
		"./dev/pkgs/dep-1": "/Users/yanick/Projects/Duct/tape/dev/pkgs/dep-1",
		"dev/pkgs":         "/Users/yanick/Projects/Duct/tape/dev/pkgs",
		".":                "/Users/yanick/Projects/Duct/tape",
		"out":              "/Users/yanick/Projects/Duct/tape/out",
	}

	for in, want := range cases {
		if got := ResolvePath(pwd, in); got != want {
			t.Errorf("ResolvePath(%q, %q) = %q, want %q", pwd, in, got, want)
		}
	}
}
