package signing

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

const signatureMagic = "tape-signature-v1"

// Errors callers distinguish between. A missing signature and a bad one need
// very different messages: one is usually a misconfigured repository, the other
// is either corruption or an attack.
var (
	ErrNoSignature   = errors.New("no signature found")
	ErrUnknownKey    = errors.New("signature was made by a key this system does not trust")
	ErrBadSignature  = errors.New("signature is not valid for this content")
	ErrContentDigest = errors.New("content does not match the digest in the signature")
	ErrWrongSubject  = errors.New("signature was made for a different repository or file")
)

// Signature is a detached signature over one file in one repository.
//
// Repo and File are part of the signed payload, so a valid signature cannot be
// lifted from one repository -- or one file -- and replayed against another.
// Without that binding, an attacker who could serve any signed artifact could
// serve a stale or unrelated one in its place.
type Signature struct {
	Repo    string
	File    string
	Sha256  string
	Created time.Time
	KeyID   string

	sig []byte
}

// payload renders the canonical bytes that are signed.
//
// Signing and verifying both rebuild this from parsed fields rather than
// hashing the file as it arrived, so incidental differences -- key order,
// spacing, line endings, trailing junk -- cannot change what a signature covers.
func (s *Signature) payload() []byte {
	var b strings.Builder
	b.WriteString(signatureMagic + "\n")
	b.WriteString("repo: " + s.Repo + "\n")
	b.WriteString("file: " + s.File + "\n")
	b.WriteString("sha256: " + s.Sha256 + "\n")
	b.WriteString("created: " + s.Created.UTC().Format(time.RFC3339) + "\n")
	b.WriteString("keyid: " + s.KeyID + "\n")
	return []byte(b.String())
}

// Marshal renders the detached signature file.
func (s *Signature) Marshal() []byte {
	return append(s.payload(), []byte("signature: "+base64.StdEncoding.EncodeToString(s.sig)+"\n")...)
}

// Sign produces a detached signature over content.
func Sign(priv *PrivateKey, repo, file string, content io.Reader, created time.Time) (*Signature, error) {
	digest, err := digestOf(content)
	if err != nil {
		return nil, err
	}

	sig := &Signature{
		Repo:    repo,
		File:    file,
		Sha256:  digest,
		Created: created.UTC().Truncate(time.Second),
		KeyID:   priv.ID,
	}
	sig.sig = ed25519.Sign(priv.Key, sig.payload())

	return sig, nil
}

// ParseSignature reads a detached signature file.
func ParseSignature(data []byte) (*Signature, error) {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, ErrNoSignature
	}

	lines := strings.Split(text, "\n")
	if strings.TrimSpace(lines[0]) != signatureMagic {
		return nil, fmt.Errorf("malformed signature: expected %q on the first line", signatureMagic)
	}

	fields := map[string]string{}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			return nil, fmt.Errorf("malformed signature: %q is not a key: value pair", line)
		}
		fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	for _, required := range []string{"repo", "file", "sha256", "created", "keyid", "signature"} {
		if _, ok := fields[required]; !ok {
			return nil, fmt.Errorf("malformed signature: missing %q", required)
		}
	}

	created, err := time.Parse(time.RFC3339, fields["created"])
	if err != nil {
		return nil, fmt.Errorf("malformed signature: bad created timestamp: %w", err)
	}

	if _, err := hex.DecodeString(fields["sha256"]); err != nil || len(fields["sha256"]) != 64 {
		return nil, fmt.Errorf("malformed signature: sha256 is not a 32-byte hex digest")
	}

	raw, err := base64.StdEncoding.DecodeString(fields["signature"])
	if err != nil {
		return nil, fmt.Errorf("malformed signature: %w", err)
	}
	if len(raw) != ed25519.SignatureSize {
		return nil, fmt.Errorf("malformed signature: %d bytes, want %d", len(raw), ed25519.SignatureSize)
	}

	return &Signature{
		Repo:    fields["repo"],
		File:    fields["file"],
		Sha256:  fields["sha256"],
		Created: created,
		KeyID:   fields["keyid"],
		sig:     raw,
	}, nil
}

// Verify checks a detached signature against content and a keyring.
//
// repo and file are what the caller expected to be verifying; a signature for
// anything else is rejected even when it is otherwise perfectly valid.
func Verify(sig *Signature, keyring *Keyring, repo, file string, content io.Reader) error {
	if sig == nil {
		return ErrNoSignature
	}

	if sig.Repo != repo || sig.File != file {
		return fmt.Errorf("%w: signature covers %s/%s, expected %s/%s",
			ErrWrongSubject, sig.Repo, sig.File, repo, file)
	}

	pub, ok := keyring.Lookup(sig.KeyID)
	if !ok {
		trusted := keyring.IDs()
		sort.Strings(trusted)
		if len(trusted) == 0 {
			return fmt.Errorf("%w: key %s, and no keys are trusted at all", ErrUnknownKey, sig.KeyID)
		}
		return fmt.Errorf("%w: key %s (trusted: %s)", ErrUnknownKey, sig.KeyID, strings.Join(trusted, ", "))
	}

	// Signature first, digest second. Checking the signature before trusting
	// any field means the digest being compared is one the key vouched for.
	if !ed25519.Verify(pub.Key, sig.payload(), sig.sig) {
		return ErrBadSignature
	}

	digest, err := digestOf(content)
	if err != nil {
		return err
	}
	if digest != sig.Sha256 {
		return fmt.Errorf("%w: got %s, signed %s", ErrContentDigest, digest, sig.Sha256)
	}

	return nil
}

func digestOf(content io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, content); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DigestFile returns the hex sha256 of a reader, for callers recording digests
// rather than verifying them.
func DigestFile(content io.Reader) (string, error) {
	return digestOf(content)
}
