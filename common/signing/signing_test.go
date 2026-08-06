package signing

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustKey(t *testing.T) *PrivateKey {
	t.Helper()
	priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func signed(t *testing.T, priv *PrivateKey, repo, file, content string) *Signature {
	t.Helper()
	sig, err := Sign(priv, repo, file, strings.NewReader(content), time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// --- happy path --------------------------------------------------------------

func TestSignAndVerify(t *testing.T) {
	priv := mustKey(t)
	kr := NewKeyring(priv.Public())

	sig := signed(t, priv, "core", "repo.db", "index contents")

	if err := Verify(sig, kr, "core", "repo.db", strings.NewReader("index contents")); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestSignatureSurvivesRoundTrip(t *testing.T) {
	priv := mustKey(t)
	kr := NewKeyring(priv.Public())

	original := signed(t, priv, "core", "repo.db", "index contents")

	parsed, err := ParseSignature(original.Marshal())
	if err != nil {
		t.Fatalf("ParseSignature: %v", err)
	}
	if err := Verify(parsed, kr, "core", "repo.db", strings.NewReader("index contents")); err != nil {
		t.Fatalf("Verify after round trip: %v", err)
	}
}

// --- the attacks -------------------------------------------------------------

// The whole point: altered content must not verify.
func TestVerifyRejectsTamperedContent(t *testing.T) {
	priv := mustKey(t)
	kr := NewKeyring(priv.Public())

	sig := signed(t, priv, "core", "repo.db", "index contents")

	err := Verify(sig, kr, "core", "repo.db", strings.NewReader("index contents (tampered)"))
	if !errors.Is(err, ErrContentDigest) {
		t.Errorf("Verify on tampered content = %v, want ErrContentDigest", err)
	}
}

// A key nobody trusts must be rejected even though the signature is internally
// valid -- this is the difference between "signed" and "signed by someone we
// trust", and the whole security property rests on it.
func TestVerifyRejectsUntrustedKey(t *testing.T) {
	attacker := mustKey(t)
	legitimate := mustKey(t)

	// The system trusts only the legitimate key.
	kr := NewKeyring(legitimate.Public())

	sig := signed(t, attacker, "core", "repo.db", "malicious index")

	err := Verify(sig, kr, "core", "repo.db", strings.NewReader("malicious index"))
	if !errors.Is(err, ErrUnknownKey) {
		t.Errorf("Verify with an untrusted key = %v, want ErrUnknownKey", err)
	}
}

func TestVerifyRejectsEmptyKeyring(t *testing.T) {
	priv := mustKey(t)
	sig := signed(t, priv, "core", "repo.db", "index")

	err := Verify(sig, NewKeyring(), "core", "repo.db", strings.NewReader("index"))
	if !errors.Is(err, ErrUnknownKey) {
		t.Errorf("Verify against an empty keyring = %v, want ErrUnknownKey", err)
	}
	// The message should say the system trusts nothing, since that is the
	// actionable problem.
	if !strings.Contains(err.Error(), "no keys are trusted") {
		t.Errorf("error %q should explain that no keys are trusted", err)
	}
}

// A signature is bound to its repository: one valid for a repo the attacker
// controls must not be replayable against a repo they do not.
func TestVerifyRejectsCrossRepoReplay(t *testing.T) {
	priv := mustKey(t)
	kr := NewKeyring(priv.Public())

	sig := signed(t, priv, "attacker-repo", "repo.db", "index contents")

	err := Verify(sig, kr, "core", "repo.db", strings.NewReader("index contents"))
	if !errors.Is(err, ErrWrongSubject) {
		t.Errorf("Verify with a signature from another repo = %v, want ErrWrongSubject", err)
	}
}

// Likewise bound to the file: a signature over one artifact must not authorise
// a different one.
func TestVerifyRejectsCrossFileReplay(t *testing.T) {
	priv := mustKey(t)
	kr := NewKeyring(priv.Public())

	sig := signed(t, priv, "core", "some-package.tape.tar.gz", "contents")

	err := Verify(sig, kr, "core", "repo.db", strings.NewReader("contents"))
	if !errors.Is(err, ErrWrongSubject) {
		t.Errorf("Verify with a signature for another file = %v, want ErrWrongSubject", err)
	}
}

// Editing the signed payload must invalidate the signature, field by field.
func TestVerifyRejectsEditedPayloadFields(t *testing.T) {
	priv := mustKey(t)
	kr := NewKeyring(priv.Public())
	other := mustKey(t)

	edits := map[string]func(*Signature){
		"sha256":  func(s *Signature) { s.Sha256 = strings.Repeat("a", 64) },
		"created": func(s *Signature) { s.Created = s.Created.Add(time.Hour) },
		"keyid":   func(s *Signature) { s.KeyID = other.ID },
	}

	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			sig := signed(t, priv, "core", "repo.db", "index contents")
			edit(sig)

			err := Verify(sig, kr, "core", "repo.db", strings.NewReader("index contents"))
			if err == nil {
				t.Fatalf("editing %s did not invalidate the signature", name)
			}
			// keyid points the lookup elsewhere; the rest fail the signature check.
			if name == "keyid" {
				if !errors.Is(err, ErrUnknownKey) {
					t.Errorf("editing keyid = %v, want ErrUnknownKey", err)
				}
				return
			}
			if !errors.Is(err, ErrBadSignature) {
				t.Errorf("editing %s = %v, want ErrBadSignature", name, err)
			}
		})
	}
}

// Swapping the raw signature bytes for another valid signature must fail.
func TestVerifyRejectsTransplantedSignature(t *testing.T) {
	priv := mustKey(t)
	kr := NewKeyring(priv.Public())

	real := signed(t, priv, "core", "repo.db", "index contents")
	decoy := signed(t, priv, "core", "repo.db", "different contents")

	real.sig = decoy.sig

	if err := Verify(real, kr, "core", "repo.db", strings.NewReader("index contents")); !errors.Is(err, ErrBadSignature) {
		t.Errorf("transplanted signature = %v, want ErrBadSignature", err)
	}
}

func TestVerifyRejectsNilSignature(t *testing.T) {
	if err := Verify(nil, NewKeyring(), "core", "repo.db", strings.NewReader("x")); !errors.Is(err, ErrNoSignature) {
		t.Errorf("Verify(nil) = %v, want ErrNoSignature", err)
	}
}

// --- parsing -----------------------------------------------------------------

func TestParseSignatureRejectsMalformedInput(t *testing.T) {
	priv := mustKey(t)
	valid := string(signed(t, priv, "core", "repo.db", "x").Marshal())

	cases := map[string]string{
		"empty":            "",
		"whitespace":       "   \n\n",
		"wrong magic":      strings.Replace(valid, signatureMagic, "not-a-tape-signature", 1),
		"missing sha256":   removeLine(valid, "sha256:"),
		"missing keyid":    removeLine(valid, "keyid:"),
		"missing sig":      removeLine(valid, "signature:"),
		"missing repo":     removeLine(valid, "repo:"),
		"bad base64":       strings.Replace(valid, "signature: ", "signature: !!!not base64!!!", 1),
		"bad timestamp":    replaceLine(valid, "created:", "created: not-a-time"),
		"short sha256":     replaceLine(valid, "sha256:", "sha256: abcd"),
		"non-hex sha256":   replaceLine(valid, "sha256:", "sha256: "+strings.Repeat("z", 64)),
		"not a key: value": valid + "\ngarbage line without a colon",
	}

	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSignature([]byte(input)); err == nil {
				t.Error("ParseSignature accepted malformed input, want error")
			}
		})
	}
}

// A truncated signature must not verify -- ed25519 signatures are fixed-size.
func TestParseSignatureRejectsTruncatedSignature(t *testing.T) {
	priv := mustKey(t)
	valid := string(signed(t, priv, "core", "repo.db", "x").Marshal())

	truncated := replaceLine(valid, "signature:", "signature: c2hvcnQ=")
	if _, err := ParseSignature([]byte(truncated)); err == nil {
		t.Error("ParseSignature accepted a short signature, want error")
	}
}

// --- keys --------------------------------------------------------------------

func TestKeyRoundTrip(t *testing.T) {
	priv := mustKey(t)

	parsedPriv, err := ParsePrivateKey(priv.Marshal())
	if err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}
	if parsedPriv.ID != priv.ID {
		t.Errorf("private key id = %q, want %q", parsedPriv.ID, priv.ID)
	}

	pub := priv.Public()
	parsedPub, err := ParsePublicKey(pub.Marshal())
	if err != nil {
		t.Fatalf("ParsePublicKey: %v", err)
	}
	if parsedPub.ID != pub.ID {
		t.Errorf("public key id = %q, want %q", parsedPub.ID, pub.ID)
	}

	// The round-tripped keys must still work together.
	kr := NewKeyring(parsedPub)
	sig := signed(t, parsedPriv, "core", "repo.db", "content")
	if err := Verify(sig, kr, "core", "repo.db", strings.NewReader("content")); err != nil {
		t.Errorf("round-tripped keys failed to verify: %v", err)
	}
}

// A key file claiming an id that does not match its material must be rejected:
// otherwise a file could be dropped in to shadow a trusted key id.
func TestParseKeyRejectsMismatchedID(t *testing.T) {
	priv := mustKey(t)

	// ReplaceAll, not Replace: Marshal writes the id in a comment as well as on
	// the key line, so replacing only the first occurrence leaves the real one
	// intact and the file still parses.
	tampered := strings.ReplaceAll(string(priv.Public().Marshal()), priv.ID, "0000000000000000")
	if _, err := ParsePublicKey([]byte(tampered)); err == nil {
		t.Error("ParsePublicKey accepted a key whose id does not match its material")
	}

	tamperedPriv := strings.ReplaceAll(string(priv.Marshal()), priv.ID, "0000000000000000")
	if _, err := ParsePrivateKey([]byte(tamperedPriv)); err == nil {
		t.Error("ParsePrivateKey accepted a key whose id does not match its material")
	}
}

func TestKeyIDIsStableAndDistinct(t *testing.T) {
	a := mustKey(t)
	b := mustKey(t)

	if a.ID == b.ID {
		t.Error("two generated keys share an id")
	}
	if KeyID(a.Public().Key) != a.ID {
		t.Error("KeyID is not stable for the same key")
	}
}

// --- keyring on disk ---------------------------------------------------------

func TestLoadKeyring(t *testing.T) {
	dir := t.TempDir()

	a, b := mustKey(t), mustKey(t)
	for _, k := range []*PrivateKey{a, b} {
		if err := WritePublicKey(filepath.Join(dir, k.ID+".pub"), k.Public()); err != nil {
			t.Fatal(err)
		}
	}
	// Non-key files must be ignored rather than fatal.
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("notes"), 0644); err != nil {
		t.Fatal(err)
	}

	kr, err := LoadKeyring(dir)
	if err != nil {
		t.Fatalf("LoadKeyring: %v", err)
	}
	if kr.Len() != 2 {
		t.Errorf("keyring holds %d keys, want 2", kr.Len())
	}
	for _, k := range []*PrivateKey{a, b} {
		if _, ok := kr.Lookup(k.ID); !ok {
			t.Errorf("key %s missing from the keyring", k.ID)
		}
	}
}

// No keys directory means no trusted keys, not a hard failure -- callers report
// it in terms of the repository they were verifying.
func TestLoadKeyringOnMissingDirectory(t *testing.T) {
	kr, err := LoadKeyring(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("LoadKeyring on a missing directory = %v, want an empty keyring", err)
	}
	if kr.Len() != 0 {
		t.Errorf("keyring holds %d keys, want 0", kr.Len())
	}
}

func TestLoadKeyringRejectsCorruptKeyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.pub"), []byte("not a key"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadKeyring(dir); err == nil {
		t.Error("LoadKeyring accepted a corrupt key file, want error")
	}
}

// A signing key readable by other users is a compromised signing key.
func TestLoadPrivateKeyRejectsLoosePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "signing.key")

	priv := mustKey(t)
	if err := os.WriteFile(path, priv.Marshal(), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadPrivateKey(path); err == nil {
		t.Error("LoadPrivateKey accepted a world-readable key, want error")
	}

	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPrivateKey(path); err != nil {
		t.Errorf("LoadPrivateKey on a 0600 key = %v, want success", err)
	}
}

// Overwriting a signing key would invalidate every repository it signed.
func TestWritePrivateKeyRefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signing.key")

	if err := WritePrivateKey(path, mustKey(t)); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateKey(path, mustKey(t)); err == nil {
		t.Error("WritePrivateKey overwrote an existing key, want error")
	}
}

func TestWritePrivateKeyUsesTightPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signing.key")
	if err := WritePrivateKey(path, mustKey(t)); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("private key mode = %v, want 0600", info.Mode().Perm())
	}
}

// --- digests -----------------------------------------------------------------

func TestDigestFile(t *testing.T) {
	// sha256 of the empty string, as a canary that the digest is really sha256.
	got, err := DigestFile(bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	const emptySha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != emptySha256 {
		t.Errorf("DigestFile(empty) = %q, want %q", got, emptySha256)
	}
}

// --- helpers -----------------------------------------------------------------

func removeLine(text, prefix string) string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func replaceLine(text, prefix, replacement string) string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			out = append(out, replacement)
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
