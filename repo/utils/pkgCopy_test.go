package utils

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"tape/common/tarUtils"
	commonUtils "tape/common/utils"
	"testing"

	"github.com/spf13/viper"
)

// The publish path used to re-tar the extraction rather than copy the input,
// and tarUtils.DefaultUntarOptions has PreserveSetuid false -- so every
// package ever published lost its setuid, setgid and sticky bits between the
// builder and the repository. Measured across the whole live index on
// 2026-08-12: 622 payloads, 251,608 entries, none carrying any of the three.
//
// Every test here carries BOTH arms. A test that only checks the setuid file
// would pass just as well against a publish step that set the bit on
// everything, which is a worse bug than the one being fixed.

// archiveEntry is one file in a synthetic package.
type archiveEntry struct {
	name string
	mode int64
	dir  bool
	body string
}

// buildArchive writes a gzipped tar of entries and returns its path.
func buildArchive(t *testing.T, dir string, entries []archiveEntry) string {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body))}
		if e.dir {
			h.Typeflag = tar.TypeDir
			h.Size = 0
		} else {
			h.Typeflag = tar.TypeReg
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatalf("writing header for %s: %v", e.name, err)
		}
		if !e.dir {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("writing body for %s: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	p := filepath.Join(dir, "input.tape.tar.gz")
	if err := os.WriteFile(p, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

// modesInArchive reads back the mode recorded for each entry.
func modesInArchive(t *testing.T, path string) map[string]int64 {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)

	modes := make(map[string]int64)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		modes[h.Name] = h.Mode
	}
	return modes
}

func digest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func testConfig() *viper.Viper {
	v := viper.New()
	v.Set("package.name", "probe")
	v.Set("package.version", "1.0.0")
	v.Set("package.subversion", "1")
	v.Set("package.arch", "x86_64")
	return v
}

// The package under test: a file that MUST keep its setuid bit, a file that
// MUST stay unprivileged, a setgid file and a sticky directory.
func probeEntries() []archiveEntry {
	return []archiveEntry{
		{name: "TAPEPACKAGE.toml", mode: 0644, body: "[package]\nname = \"probe\"\n"},
		{name: "install/usr/bin/needs-setuid", mode: 04755, body: "suid"},
		{name: "install/usr/bin/stays-plain", mode: 0755, body: "plain"},
		{name: "install/usr/bin/needs-setgid", mode: 02755, body: "sgid"},
		{name: "install/tmp/", mode: 01777, dir: true},
	}
}

func TestPkgCopyPreservesSpecialBitsAndLeavesPlainFilesAlone(t *testing.T) {
	src := t.TempDir()
	repo := t.TempDir()

	in := buildArchive(t, src, probeEntries())
	out, err := PkgCopy(in, testConfig(), repo)
	if err != nil {
		t.Fatalf("PkgCopy: %v", err)
	}

	modes := modesInArchive(t, out)

	// Arm one: the bits that must survive. This is the regression this change
	// exists for -- each of these was 0755 or 0777 in every published package.
	for name, want := range map[string]int64{
		"install/usr/bin/needs-setuid": 04755,
		"install/usr/bin/needs-setgid": 02755,
		"install/tmp/":                 01777,
	} {
		if got, ok := modes[name]; !ok {
			t.Errorf("%s is missing from the published archive", name)
		} else if got != want {
			t.Errorf("%s published as %04o, want %04o -- the publish path is dropping special bits again", name, got, want)
		}
	}

	// Arm two: the file that must NOT gain anything. Without this, a publish
	// step that set 04755 on everything would pass arm one.
	if got := modes["install/usr/bin/stays-plain"]; got != 0755 {
		t.Errorf("install/usr/bin/stays-plain published as %04o, want 0755 -- publishing must not grant privilege", got)
	}
}

// The bits are the symptom; rebuilding the artefact was the defect. Byte
// identity is what makes the whole class impossible rather than this one
// instance, and it is what lets the recorded digest describe the artefact the
// builder produced.
func TestPkgCopyPublishesTheInputBytesUnchanged(t *testing.T) {
	src := t.TempDir()
	repo := t.TempDir()

	in := buildArchive(t, src, probeEntries())
	out, err := PkgCopy(in, testConfig(), repo)
	if err != nil {
		t.Fatalf("PkgCopy: %v", err)
	}

	if want, got := digest(t, in), digest(t, out); want != got {
		t.Errorf("published bytes differ from the input:\n  input     %s\n  published %s", want, got)
	}
}

// THE WITNESS. Everything above is green against the fixed code, and a green
// assertion that has never been watched fail is not evidence. This reproduces
// what publishing used to do -- extract with tarUtils.DefaultUntarOptions,
// re-tar the extraction -- and asserts the bits ARE lost, so the assertions in
// TestPkgCopyPreservesSpecialBitsAndLeavesPlainFilesAlone are demonstrably
// capable of failing rather than merely passing.
//
// It is also a live check on the surrounding landscape. If tarUtils ever
// starts preserving these bits by default, this test fails and says so, which
// is the moment someone would otherwise conclude the publish path had been
// safe all along.
func TestTheOldRetarPathLosesSpecialBits(t *testing.T) {
	src := t.TempDir()
	staging := t.TempDir()
	repo := t.TempDir()

	in := buildArchive(t, src, probeEntries())

	f, err := os.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	opts := tarUtils.DefaultUntarOptions
	opts.AllowLinks = true
	if err := tarUtils.UntarWithOptions(staging, f, opts); err != nil {
		t.Fatalf("extracting: %v", err)
	}
	if err := commonUtils.PkgBuildTar("old.tape.tar.gz", staging, repo); err != nil {
		t.Fatalf("re-taring: %v", err)
	}

	modes := modesInArchive(t, filepath.Join(repo, "old.tape.tar.gz"))

	for name, lost := range map[string]int64{
		"install/usr/bin/needs-setuid": 04000,
		"install/usr/bin/needs-setgid": 02000,
		"install/tmp/":                 01000,
	} {
		got, ok := modes[name]
		if !ok {
			t.Fatalf("%s missing from the re-tar; the witness is not measuring what it claims", name)
		}
		if got&lost != 0 {
			t.Errorf("%s kept %04o through the old extract-and-re-tar path (mode %04o).\n"+
				"The defect this package was changed to fix no longer reproduces, so the\n"+
				"tests above are passing for a reason nobody has checked.", name, lost, got)
		}
	}
}

// The published file has to be readable by whatever serves the repository.
// os.CreateTemp makes 0600, so this is a real thing to get wrong.
func TestPkgCopyPublishesWorldReadable(t *testing.T) {
	src := t.TempDir()
	repo := t.TempDir()

	out, err := PkgCopy(buildArchive(t, src, probeEntries()), testConfig(), repo)
	if err != nil {
		t.Fatalf("PkgCopy: %v", err)
	}
	fi, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0044 == 0 {
		t.Errorf("published package is %04o; a file server cannot read it", fi.Mode().Perm())
	}
}

// The name is derived from the manifest, not from the input file name -- a
// package handed over under any name must publish under its canonical one,
// because that is the name the index records and the client requests.
func TestPkgCopyNamesTheArchiveFromTheManifest(t *testing.T) {
	src := t.TempDir()
	repo := t.TempDir()

	out, err := PkgCopy(buildArchive(t, src, probeEntries()), testConfig(), repo)
	if err != nil {
		t.Fatalf("PkgCopy: %v", err)
	}
	if want := "probe-1.0.0-1.x86_64.tape.tar.gz"; filepath.Base(out) != want {
		t.Errorf("published as %q, want %q", filepath.Base(out), want)
	}
}

// A failure part-way through must not leave a partial package where a complete
// one is expected -- the temporary file is written in the destination
// directory, so a leaked one would be served.
func TestPkgCopyLeavesNoPartialFileWhenTheSourceIsUnreadable(t *testing.T) {
	repo := t.TempDir()

	if _, err := PkgCopy(filepath.Join(t.TempDir(), "does-not-exist"), testConfig(), repo); err == nil {
		t.Fatal("PkgCopy accepted a missing source")
	}

	entries, err := os.ReadDir(filepath.Join(repo, "packages"))
	if err != nil {
		if os.IsNotExist(err) {
			return // nothing created at all is the best outcome
		}
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("a failed publish left %q behind", e.Name())
	}
}
