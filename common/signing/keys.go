// Package signing provides the chain of trust for repository metadata and
// package archives.
//
// The chain is: a trusted public key verifies a detached signature over a
// repository's index, and the index carries a sha256 for every package it
// lists. Verifying one signature therefore covers every artifact that
// repository serves -- packages do not need individual signatures, only an
// index that cannot be tampered with.
//
// Ed25519 is used throughout, via crypto/ed25519 in the standard library.
package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	publicKeyMagic  = "tape-ed25519-public-key-v1"
	privateKeyMagic = "tape-ed25519-private-key-v1"
)

// PublicKey is a key trusted to sign repository indexes.
type PublicKey struct {
	ID  string
	Key ed25519.PublicKey
}

// PrivateKey signs repository indexes. It is held by whoever publishes a
// repository, never by the machine installing from one.
type PrivateKey struct {
	ID  string
	Key ed25519.PrivateKey
}

// Public returns the verifying half of a signing key.
func (p *PrivateKey) Public() *PublicKey {
	return &PublicKey{
		ID:  p.ID,
		Key: p.Key.Public().(ed25519.PublicKey),
	}
}

// KeyID derives a short, stable identifier from a public key.
//
// Deriving it from the key rather than assigning one means a key file cannot
// claim an identity that does not match its material: changing the ID changes
// the lookup, and the signature then fails against whatever key is actually
// found.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// GenerateKey creates a new signing keypair.
func GenerateKey() (*PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &PrivateKey{ID: KeyID(pub), Key: priv}, nil
}

// Marshal renders a public key in its on-disk form.
func (p *PublicKey) Marshal() []byte {
	var b strings.Builder
	b.WriteString("# tape public key\n")
	b.WriteString("# id: " + p.ID + "\n")
	b.WriteString(publicKeyMagic + " " + p.ID + " " + base64.StdEncoding.EncodeToString(p.Key) + "\n")
	return []byte(b.String())
}

// Marshal renders a private key in its on-disk form.
//
// The key material is stored unencrypted. Protect the file with its
// permissions (WritePrivateKey uses 0600) and keep it off the machines that
// install packages; a passphrase-wrapped format would be better and is not
// implemented yet.
func (p *PrivateKey) Marshal() []byte {
	seed := p.Key.Seed()

	var b strings.Builder
	b.WriteString("# tape private key -- KEEP SECRET, stored unencrypted\n")
	b.WriteString("# id: " + p.ID + "\n")
	b.WriteString(privateKeyMagic + " " + p.ID + " " + base64.StdEncoding.EncodeToString(seed) + "\n")
	return []byte(b.String())
}

// ParsePublicKey reads a public key file.
func ParsePublicKey(data []byte) (*PublicKey, error) {
	id, raw, err := parseKeyLine(data, publicKeyMagic)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}

	pub := ed25519.PublicKey(raw)

	// The stated id must match the key material, or lookups and signatures
	// could disagree about which key is in play.
	if derived := KeyID(pub); derived != id {
		return nil, fmt.Errorf("key id %q does not match the key material (derived %q)", id, derived)
	}

	return &PublicKey{ID: id, Key: pub}, nil
}

// ParsePrivateKey reads a private key file.
func ParsePrivateKey(data []byte) (*PrivateKey, error) {
	id, seed, err := parseKeyLine(data, privateKeyMagic)
	if err != nil {
		return nil, err
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("private key seed is %d bytes, want %d", len(seed), ed25519.SeedSize)
	}

	priv := ed25519.NewKeyFromSeed(seed)
	derived := KeyID(priv.Public().(ed25519.PublicKey))
	if derived != id {
		return nil, fmt.Errorf("key id %q does not match the key material (derived %q)", id, derived)
	}

	return &PrivateKey{ID: id, Key: priv}, nil
}

// parseKeyLine finds the single significant line of a key file.
func parseKeyLine(data []byte, magic string) (id string, raw []byte, err error) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != magic {
			return "", nil, fmt.Errorf("malformed key file: expected %q followed by an id and base64 key material", magic)
		}

		decoded, err := base64.StdEncoding.DecodeString(fields[2])
		if err != nil {
			return "", nil, fmt.Errorf("malformed key file: %w", err)
		}
		return fields[1], decoded, nil
	}

	return "", nil, fmt.Errorf("malformed key file: no %q line found", magic)
}

// WritePublicKey writes a public key where the daemon can find it.
func WritePublicKey(path string, pub *PublicKey) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, pub.Marshal(), 0644)
}

// WritePrivateKey writes a signing key, readable only by its owner.
func WritePrivateKey(path string, priv *PrivateKey) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// O_EXCL: never silently overwrite a signing key. Losing one means every
	// repository it signed has to be re-signed.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Write(priv.Marshal()); err != nil {
		return err
	}
	return f.Sync()
}

// LoadPrivateKey reads a signing key, refusing one that is readable by others.
func LoadPrivateKey(path string) (*PrivateKey, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("private key %s is accessible by other users (mode %v); chmod 600 it", path, info.Mode().Perm())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePrivateKey(data)
}

// Keyring is the set of public keys a system trusts.
type Keyring struct {
	keys map[string]*PublicKey
}

// NewKeyring builds a keyring from keys already in memory.
func NewKeyring(keys ...*PublicKey) *Keyring {
	kr := &Keyring{keys: make(map[string]*PublicKey, len(keys))}
	for _, k := range keys {
		kr.keys[k.ID] = k
	}
	return kr
}

// LoadKeyring reads every .pub file in dir.
//
// A missing directory yields an empty keyring rather than an error: that is a
// system with no trusted keys, which callers report in terms of the repository
// they were trying to verify.
func LoadKeyring(dir string) (*Keyring, error) {
	kr := &Keyring{keys: map[string]*PublicKey{}}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return kr, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pub") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		pub, err := ParsePublicKey(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		kr.keys[pub.ID] = pub
	}

	return kr, nil
}

// Lookup finds a trusted key by id.
func (k *Keyring) Lookup(id string) (*PublicKey, bool) {
	pub, ok := k.keys[id]
	return pub, ok
}

// Len reports how many keys are trusted.
func (k *Keyring) Len() int { return len(k.keys) }

// IDs lists the trusted key ids, for diagnostics.
func (k *Keyring) IDs() []string {
	out := make([]string, 0, len(k.keys))
	for id := range k.keys {
		out = append(out, id)
	}
	return out
}
