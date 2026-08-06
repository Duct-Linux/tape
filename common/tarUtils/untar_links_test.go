package tarUtils

import (
	"archive/tar"
	"os"
	"path/filepath"
	"testing"
)

func linksAllowed() UntarOptions {
	opts := DefaultUntarOptions
	opts.AllowLinks = true
	return opts
}

// The default policy refuses links outright, which makes the rejection tests
// pass for the wrong reason. These exercise the containment logic itself.

func TestUntarLinksEnabledAllowsInternalSymlink(t *testing.T) {
	_, dst := dstDir(t)

	r := makeTarGz(t, []entry{
		{name: "real.txt", body: "payload"},
		{name: "alias.txt", typeflag: tar.TypeSymlink, linkname: "real.txt"},
	})

	if err := UntarWithOptions(dst, r, linksAllowed()); err != nil {
		t.Fatalf("UntarWithOptions: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "alias.txt"))
	if err != nil {
		t.Fatalf("reading through internal symlink: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("content = %q, want %q", got, "payload")
	}
}

func TestUntarLinksEnabledStillRejectsAbsoluteTarget(t *testing.T) {
	_, dst := dstDir(t)

	r := makeTarGz(t, []entry{
		{name: "evil", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
	})
	if err := UntarWithOptions(dst, r, linksAllowed()); err == nil {
		t.Fatal("absolute symlink target accepted with links enabled, want error")
	}
}

func TestUntarLinksEnabledStillRejectsEscapingTarget(t *testing.T) {
	parent, dst := dstDir(t)

	r := makeTarGz(t, []entry{
		{name: "evil", typeflag: tar.TypeSymlink, linkname: "../outside-secret"},
	})
	if err := UntarWithOptions(dst, r, linksAllowed()); err == nil {
		t.Fatal("escaping symlink target accepted with links enabled, want error")
	}
	if _, err := os.Lstat(filepath.Join(parent, "outside-secret")); err == nil {
		t.Fatal("symlink escape materialised outside the destination")
	}
}

func TestUntarLinksEnabledRejectsEscapingHardlink(t *testing.T) {
	_, dst := dstDir(t)

	r := makeTarGz(t, []entry{
		{name: "evil", typeflag: tar.TypeLink, linkname: "../../etc/passwd"},
	})
	if err := UntarWithOptions(dst, r, linksAllowed()); err == nil {
		t.Fatal("escaping hardlink target accepted with links enabled, want error")
	}
}

// The dangerous case that does not come from the archive at all: a symlink
// already sitting in the destination, pointing outward. A plain O_NOFOLLOW on
// the final component does not catch a write through a symlinked *parent*.
func TestUntarRefusesWriteThroughPrePlantedParentSymlink(t *testing.T) {
	parent, dst := dstDir(t)

	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	// An attacker (or a previous extraction) plants this before we run.
	if err := os.Symlink(outside, filepath.Join(dst, "etc")); err != nil {
		t.Fatal(err)
	}

	r := makeTarGz(t, []entry{{name: "etc/pwned.conf", body: "pwned"}})

	if err := Untar(dst, r); err == nil {
		t.Fatal("write through a pre-planted parent symlink was accepted, want error")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.conf")); err == nil {
		t.Fatal("write escaped through a pre-planted parent symlink")
	}
}

// Same shape, but the final component is the planted symlink -- this is the
// O_NOFOLLOW case.
func TestUntarRefusesWriteThroughPrePlantedFileSymlink(t *testing.T) {
	parent, dst := dstDir(t)

	victim := filepath.Join(parent, "victim.txt")
	if err := os.WriteFile(victim, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dst, "innocent.txt")); err != nil {
		t.Fatal(err)
	}

	r := makeTarGz(t, []entry{{name: "innocent.txt", body: "overwritten"}})

	if err := Untar(dst, r); err == nil {
		t.Fatal("write through a pre-planted file symlink was accepted, want error")
	}

	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("victim file was overwritten through a symlink: %q", got)
	}
}

func TestUntarPreserveSetuidOptIn(t *testing.T) {
	_, dst := dstDir(t)

	opts := DefaultUntarOptions
	opts.PreserveSetuid = true

	r := makeTarGz(t, []entry{{name: "helper", mode: 04755, body: "x"}})
	if err := UntarWithOptions(dst, r, opts); err != nil {
		t.Fatalf("UntarWithOptions: %v", err)
	}

	fi, err := os.Stat(filepath.Join(dst, "helper"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSetuid == 0 {
		t.Error("setuid bit was dropped despite PreserveSetuid")
	}
}
