package utils

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"tape/common/signing"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// repoFixture is a repository directory plus the key that signs it.
type repoFixture struct {
	dir      string
	keysDir  string
	priv     *signing.PrivateKey
	indexPth string
	sigPath  string
}

func newRepoFixture(t *testing.T, repoName, indexContent string) *repoFixture {
	t.Helper()

	root := t.TempDir()
	dir := filepath.Join(root, "repo")
	keysDir := filepath.Join(root, "keys")
	for _, d := range []string{dir, keysDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	priv, err := signing.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := signing.WritePublicKey(filepath.Join(keysDir, priv.ID+".pub"), priv.Public()); err != nil {
		t.Fatal(err)
	}

	indexPath := filepath.Join(dir, RepoDbName)
	if err := os.WriteFile(indexPath, []byte(indexContent), 0644); err != nil {
		t.Fatal(err)
	}

	f := &repoFixture{
		dir:      dir,
		keysDir:  keysDir,
		priv:     priv,
		indexPth: indexPath,
		sigPath:  filepath.Join(dir, RepoSigName),
	}
	f.sign(t, repoName, priv)

	// Point the daemon's key lookup at this fixture.
	t.Setenv("TAPE_CONFIG_DIR", root)
	if err := os.MkdirAll(filepath.Join(root, "keys"), 0755); err != nil {
		t.Fatal(err)
	}

	return f
}

func (f *repoFixture) sign(t *testing.T, repoName string, key *signing.PrivateKey) {
	t.Helper()

	index, err := os.Open(f.indexPth)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()

	sig, err := signing.Sign(key, repoName, RepoDbName, index, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.sigPath, sig.Marshal(), 0644); err != nil {
		t.Fatal(err)
	}
}

func repoConfig(key string, allowUnsigned bool) *viper.Viper {
	v := viper.New()
	v.Set("key", key)
	v.Set("repo.allow-unsigned", allowUnsigned)
	return v
}

func TestVerifyRepoIndexAcceptsAGoodSignature(t *testing.T) {
	f := newRepoFixture(t, "core", "index contents")

	if err := VerifyRepoIndex(repoConfig("core", false), f.indexPth, f.sigPath); err != nil {
		t.Fatalf("VerifyRepoIndex on a correctly signed repo = %v", err)
	}
}

// The attack this whole feature exists to stop: a MITM serves a modified index.
func TestVerifyRepoIndexRejectsTamperedIndex(t *testing.T) {
	f := newRepoFixture(t, "core", "index contents")

	if err := os.WriteFile(f.indexPth, []byte("malicious index"), 0644); err != nil {
		t.Fatal(err)
	}

	err := VerifyRepoIndex(repoConfig("core", false), f.indexPth, f.sigPath)
	if err == nil {
		t.Fatal("VerifyRepoIndex accepted a tampered index")
	}
	if !errors.Is(err, signing.ErrContentDigest) {
		t.Errorf("error = %v, want ErrContentDigest", err)
	}
}

// An attacker with their own key must not be able to sign their way in.
func TestVerifyRepoIndexRejectsUntrustedSigner(t *testing.T) {
	f := newRepoFixture(t, "core", "index contents")

	attacker, err := signing.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	// Attacker replaces both index and signature with their own.
	if err := os.WriteFile(f.indexPth, []byte("malicious index"), 0644); err != nil {
		t.Fatal(err)
	}
	f.sign(t, "core", attacker)

	err = VerifyRepoIndex(repoConfig("core", false), f.indexPth, f.sigPath)
	if !errors.Is(err, signing.ErrUnknownKey) {
		t.Errorf("error = %v, want ErrUnknownKey", err)
	}
}

// A signature legitimately made for another repository must not be reusable.
func TestVerifyRepoIndexRejectsSignatureFromAnotherRepo(t *testing.T) {
	f := newRepoFixture(t, "other-repo", "index contents")

	err := VerifyRepoIndex(repoConfig("core", false), f.indexPth, f.sigPath)
	if !errors.Is(err, signing.ErrWrongSubject) {
		t.Errorf("error = %v, want ErrWrongSubject", err)
	}
}

// Simply deleting the signature must not downgrade a repository to unverified.
func TestVerifyRepoIndexRejectsStrippedSignature(t *testing.T) {
	f := newRepoFixture(t, "core", "index contents")

	if err := os.Remove(f.sigPath); err != nil {
		t.Fatal(err)
	}

	err := VerifyRepoIndex(repoConfig("core", false), f.indexPth, f.sigPath)
	if !errors.Is(err, ErrUnsigned) {
		t.Errorf("error = %v, want ErrUnsigned", err)
	}
	// The message has to say what to do about it.
	if !strings.Contains(err.Error(), "allow-unsigned") {
		t.Errorf("error %q should mention the allow-unsigned escape hatch", err)
	}
}

func TestVerifyRepoIndexRejectsCorruptSignatureFile(t *testing.T) {
	f := newRepoFixture(t, "core", "index contents")

	if err := os.WriteFile(f.sigPath, []byte("this is not a signature"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := VerifyRepoIndex(repoConfig("core", false), f.indexPth, f.sigPath); err == nil {
		t.Error("VerifyRepoIndex accepted a corrupt signature file")
	}
}

// The opt-out has to work, since it is the documented escape hatch -- but only
// when set explicitly.
func TestVerifyRepoIndexHonoursAllowUnsigned(t *testing.T) {
	f := newRepoFixture(t, "core", "index contents")
	if err := os.Remove(f.sigPath); err != nil {
		t.Fatal(err)
	}

	if err := VerifyRepoIndex(repoConfig("core", true), f.indexPth, f.sigPath); err != nil {
		t.Errorf("allow-unsigned repo = %v, want nil", err)
	}
}

// With no trusted keys installed, a signed repository must still be refused --
// "signed by somebody" is not the property that matters.
func TestVerifyRepoIndexRejectsWhenNoKeysAreTrusted(t *testing.T) {
	f := newRepoFixture(t, "core", "index contents")

	// Point the key lookup at an empty directory.
	empty := t.TempDir()
	t.Setenv("TAPE_CONFIG_DIR", empty)

	err := VerifyRepoIndex(repoConfig("core", false), f.indexPth, f.sigPath)
	if !errors.Is(err, signing.ErrUnknownKey) {
		t.Errorf("error = %v, want ErrUnknownKey", err)
	}
}

// --- package digests ---------------------------------------------------------

func TestVerifyPkgDigest(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "pkg.tape.tar.gz")
	content := []byte("package archive contents")
	if err := os.WriteFile(archive, content, 0644); err != nil {
		t.Fatal(err)
	}

	digest, err := signing.DigestFile(strings.NewReader(string(content)))
	if err != nil {
		t.Fatal(err)
	}

	if err := VerifyPkgDigest(archive, digest, int64(len(content)), false); err != nil {
		t.Fatalf("VerifyPkgDigest on a matching archive = %v", err)
	}
}

func TestVerifyPkgDigestRejectsTamperedArchive(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "pkg.tape.tar.gz")

	digest, err := signing.DigestFile(strings.NewReader("the real package"))
	if err != nil {
		t.Fatal(err)
	}

	// Same length, different bytes: the size check passes and the digest must
	// be what catches it.
	if err := os.WriteFile(archive, []byte("the fake package"), 0644); err != nil {
		t.Fatal(err)
	}

	err = VerifyPkgDigest(archive, digest, int64(len("the real package")), false)
	if err == nil {
		t.Fatal("VerifyPkgDigest accepted a tampered archive")
	}
	if !strings.Contains(err.Error(), "digest mismatch") {
		t.Errorf("error %q should report a digest mismatch", err)
	}
}

func TestVerifyPkgDigestRejectsWrongSize(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "pkg.tape.tar.gz")
	if err := os.WriteFile(archive, []byte("short"), 0644); err != nil {
		t.Fatal(err)
	}

	err := VerifyPkgDigest(archive, strings.Repeat("a", 64), 99999, false)
	if err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Errorf("error = %v, want a size mismatch", err)
	}
}

// An index with no digest means the repository predates digest support. That is
// a refusal, not a silent pass.
func TestVerifyPkgDigestRejectsMissingDigest(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "pkg.tape.tar.gz")
	if err := os.WriteFile(archive, []byte("contents"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := VerifyPkgDigest(archive, "", 0, false); err == nil {
		t.Error("VerifyPkgDigest accepted a package with no recorded digest")
	}

	// ...unless the repository is explicitly unsigned.
	if err := VerifyPkgDigest(archive, "", 0, true); err != nil {
		t.Errorf("allow-unsigned repo with no digest = %v, want nil", err)
	}
}
