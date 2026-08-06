package structs

import (
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func validRefMap() map[string]string {
	return map[string]string{
		"repo":       "core",
		"name":       "bash",
		"version":    "5.2",
		"subversion": "1",
		"arch":       "x86_64",
	}
}

func TestPkgRefFromMapAcceptsValidCoordinates(t *testing.T) {
	ref, err := PkgRefFromMap(validRefMap())
	if err != nil {
		t.Fatalf("PkgRefFromMap() error = %v", err)
	}
	if ref.Name != "bash" || ref.Repo != "core" || ref.Arch != "x86_64" {
		t.Errorf("unexpected ref %+v", ref)
	}
}

func TestPkgRefFromMapRejectsNil(t *testing.T) {
	if _, err := PkgRefFromMap(nil); err == nil {
		t.Error("PkgRefFromMap(nil) = nil error, want error")
	}
}

// These are the exact payloads that gave an unprivileged caller an arbitrary
// root-owned file write.
func TestPkgRefFromMapRejectsTraversal(t *testing.T) {
	hostile := []struct {
		field string
		value string
	}{
		{"repo", "../../tmp/evil"},
		{"repo", "/etc/tape/repos/core"},
		{"repo", ".."},
		{"name", "../../../../etc/cron.d/pwn"},
		{"name", "/etc/passwd"},
		{"name", "a/../../b"},
		{"version", "../1.0"},
		{"subversion", "../../1"},
		{"arch", "../x86_64"},
		{"arch", "x86_64/.."},
	}

	for _, h := range hostile {
		t.Run(h.field+"="+h.value, func(t *testing.T) {
			m := validRefMap()
			m[h.field] = h.value
			if _, err := PkgRefFromMap(m); err == nil {
				t.Errorf("PkgRefFromMap with %s=%q = nil error, want error", h.field, h.value)
			}
		})
	}
}

func TestPkgRefFromMapRejectsMissingFields(t *testing.T) {
	for _, field := range []string{"repo", "name", "version", "subversion", "arch"} {
		t.Run("missing "+field, func(t *testing.T) {
			m := validRefMap()
			delete(m, field)
			if _, err := PkgRefFromMap(m); err == nil {
				t.Errorf("PkgRefFromMap without %q = nil error, want error", field)
			}
		})
	}
}

func TestPkgRefRoundTripsThroughMap(t *testing.T) {
	original, err := PkgRefFromMap(validRefMap())
	if err != nil {
		t.Fatal(err)
	}

	round, err := PkgRefFromMap(original.ToMap())
	if err != nil {
		t.Fatalf("round trip failed: %v", err)
	}
	if round != original {
		t.Errorf("round trip changed the ref: %+v -> %+v", original, round)
	}
}

// The invariant the daemon actually depends on: a validated ref cannot steer a
// Join or a URL join outside its intended base.
func TestValidatedRefCannotEscapePaths(t *testing.T) {
	ref, err := PkgRefFromMap(validRefMap())
	if err != nil {
		t.Fatal(err)
	}

	repoBase := "/etc/tape/repos"
	repoPath := filepath.Join(repoBase, ref.Repo+".toml")
	if !strings.HasPrefix(repoPath, repoBase+"/") {
		t.Errorf("repo config path %q escaped %q", repoPath, repoBase)
	}

	tmpBase := "/tmp/tape/abcdef"
	fileName := ref.Name + "-" + ref.Version + "-" + ref.Subversion + "." + ref.Arch + ".tape.tar.gz"
	pkgPath := path.Join(tmpBase, fileName)
	if !strings.HasPrefix(pkgPath, tmpBase+"/") {
		t.Errorf("package path %q escaped %q", pkgPath, tmpBase)
	}
}
