package utils

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateNameRejectsTraversal(t *testing.T) {
	// Each of these reached a root-privileged filepath.Join or os.Create.
	hostile := []string{
		"../../tmp/evil",
		"..",
		"../etc/passwd",
		"a/../../b",
		"/etc/passwd",
		"/absolute",
		"sub/dir",
		`back\slash`,
		"trailing/",
		"",
		".",
		"..hidden/../..",
		"nul\x00byte",
		"tab\there",
		"new\nline",
		"semi;colon",
		"dollar$sign",
		"back`tick`",
		"space here",
	}

	for _, name := range hostile {
		t.Run(name, func(t *testing.T) {
			if err := ValidateName(name); err == nil {
				t.Errorf("ValidateName(%q) = nil, want error", name)
			}
		})
	}
}

func TestValidateNameAcceptsRealPackageNames(t *testing.T) {
	valid := []string{
		"bash",
		"gcc",
		"core",
		"test-dev",
		"dep-1",
		"dep-2-1",
		"lib32-glibc",
		"python3.11",
		"gtk+",
		"a",
		"linux-firmware",
		"ca-certificates_1",
	}

	for _, name := range valid {
		t.Run(name, func(t *testing.T) {
			if err := ValidateName(name); err != nil {
				t.Errorf("ValidateName(%q) = %v, want nil", name, err)
			}
		})
	}
}

func TestValidateNameRejectsLeadingPunctuation(t *testing.T) {
	// A leading '-' can be read as a flag by anything that shells out; a
	// leading '.' hides the file and enables "." / ".." confusion.
	for _, name := range []string{"-rf", "-", ".hidden", ".", "..", "+x"} {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) = nil, want error", name)
		}
	}
}

func TestValidateNameRejectsOverlongInput(t *testing.T) {
	if err := ValidateName(strings.Repeat("a", 1024)); err == nil {
		t.Error("ValidateName accepted a 1024-character name, want error")
	}
}

func TestValidateVersion(t *testing.T) {
	valid := []string{"1.0", "1.0.0", "2.1", "1.0.0-rc1", "1.0.0+build.5", "1:2.3.4", "1.0~beta"}
	for _, v := range valid {
		if err := ValidateVersion(v); err != nil {
			t.Errorf("ValidateVersion(%q) = %v, want nil", v, err)
		}
	}

	invalid := []string{"", "../1.0", "1.0/../2.0", "1.0 ; rm -rf /", "/1.0", "1.0\x00"}
	for _, v := range invalid {
		if err := ValidateVersion(v); err == nil {
			t.Errorf("ValidateVersion(%q) = nil, want error", v)
		}
	}
}

func TestValidateArch(t *testing.T) {
	for _, a := range []string{"x86_64", "i686", "aarch64", "arm", "any"} {
		if err := ValidateArch(a); err != nil {
			t.Errorf("ValidateArch(%q) = %v, want nil", a, err)
		}
	}
	for _, a := range []string{"", "../x86_64", "x86_64/..", "x86 64", "x86;64"} {
		if err := ValidateArch(a); err == nil {
			t.Errorf("ValidateArch(%q) = nil, want error", a)
		}
	}
}

// The property that actually matters: a validated name can never widen the
// directory a Join lands in.
func TestValidatedNamesCannotEscapeAJoin(t *testing.T) {
	base := "/etc/tape/repos"

	for _, name := range []string{"core", "test-dev", "big", "python3.11", "gtk+"} {
		if err := ValidateName(name); err != nil {
			t.Fatalf("fixture %q should be valid: %v", name, err)
		}
		joined := filepath.Join(base, name+".toml")
		if !strings.HasPrefix(joined, base+"/") {
			t.Errorf("Join(%q, %q) = %q escaped the base", base, name, joined)
		}
	}
}
