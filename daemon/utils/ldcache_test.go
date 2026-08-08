package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"tape/common/logger"
	"testing"
)

func TestTouchesSharedLibrary(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"usr/lib/libc.so", true},
		{"usr/lib/libc.so.6", true},
		{"usr/lib/libfoo.so.1.2.3", true},
		{"usr/lib/ld-linux-x86-64.so.2", true},
		{"usr/bin/sed", false},
		{"usr/share/doc/README", false},
		// ".so" has to end the stem or be followed by a version, or every
		// file with those letters mid-name triggers a rebuild.
		{"usr/share/locale/so/LC_MESSAGES/x.mo", false},
		{"usr/lib/libfoo.sox", false},
		{"usr/share/x.sound", false},
		{".so", false}, // no stem: not a library
	} {
		if got := touchesSharedLibrary([]string{tc.path}); got != tc.want {
			t.Errorf("touchesSharedLibrary(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}

	if touchesSharedLibrary([]string{"usr/bin/a", "usr/lib/libz.so.1"}) != true {
		t.Error("a shared library anywhere in the set should trigger a refresh")
	}
	if touchesSharedLibrary(nil) != false {
		t.Error("no paths should not trigger a refresh")
	}
}

func TestFindLdconfigPrefersSysrootAndRequiresExecutable(t *testing.T) {
	root := t.TempDir()
	if findLdconfig(root) != "" {
		t.Error("empty sysroot should yield no ldconfig")
	}

	// Present but not executable: not usable.
	if err := os.MkdirAll(filepath.Join(root, "usr/sbin"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "usr/sbin/ldconfig")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := findLdconfig(root); got != "" {
		t.Errorf("non-executable ldconfig should be ignored, got %q", got)
	}

	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findLdconfig(root); got != p {
		t.Errorf("findLdconfig = %q, want %q", got, p)
	}
}

// A missing or failing ldconfig must never turn a committed install into an
// error -- the files are already on disk and recorded.
func TestRefreshLinkerCacheNeverPanicsOrFailsLoudly(t *testing.T) {
	log := logger.NewLogger("test", "ldcache")

	// No ldconfig at all.
	RefreshLinkerCache(t.TempDir(), []string{"usr/lib/libz.so.1"}, log)

	// One that exits non-zero.
	if runtime.GOOS != "windows" {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "usr/sbin"), 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(root, "usr/sbin/ldconfig")
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho boom >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		RefreshLinkerCache(root, []string{"usr/lib/libz.so.1"}, log)
	}
}

// The refresh must actually run when a library lands, and be skipped when none
// does -- the whole point is that it stops being a manual step.
func TestRefreshLinkerCacheRunsOnlyForLibraries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	log := logger.NewLogger("test", "ldcache")

	newRoot := func(t *testing.T) (string, string) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "usr/sbin"), 0o755); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(root, "ran")
		stub := "#!/bin/sh\necho \"$@\" > " + marker + "\n"
		if err := os.WriteFile(filepath.Join(root, "usr/sbin/ldconfig"), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
		return root, marker
	}

	root, marker := newRoot(t)
	RefreshLinkerCache(root, []string{"usr/lib/libz.so.1"}, log)
	args, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("ldconfig was not run for a package shipping a library: %v", err)
	}
	// A non-/ sysroot must be passed through, or it indexes the host instead.
	if !strings.Contains(string(args), "-r "+root) {
		t.Errorf("ldconfig args = %q, want -r %s", strings.TrimSpace(string(args)), root)
	}

	root2, marker2 := newRoot(t)
	RefreshLinkerCache(root2, []string{"usr/bin/sed", "usr/share/man/man1/sed.1"}, log)
	if _, err := os.Stat(marker2); err == nil {
		t.Error("ldconfig ran for a package with no shared libraries")
	}
}
