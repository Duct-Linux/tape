package utils

import (
	"errors"
	"fmt"
	"os"
	"tape/common/config"
	"tape/common/logger"
	"tape/common/signing"

	"github.com/spf13/viper"
)

// Names of the files a repository publishes.
const (
	RepoDbName  = "repo.db"
	RepoSigName = RepoDbName + ".sig"
)

// ErrUnsigned reports a repository that supplied no signature at all.
var ErrUnsigned = errors.New("repository is not signed")

// VerifyRepoIndex checks a downloaded index against the system keyring.
//
// This is the root of the trust chain. Everything else -- which packages exist,
// what versions they are, and the digest of every archive -- comes out of this
// file, and the daemon opens it as root. If it cannot be attributed to a
// trusted key, nothing derived from it can be trusted either.
func VerifyRepoIndex(repo *viper.Viper, indexPath, sigPath string) error {
	log := logger.NewLogger("daemon", "utils.VerifyRepoIndex")

	repoKey := repo.GetString("key")

	if repo.GetBool("repo.allow-unsigned") {
		// Explicitly opted out, per repository. Say so on every refresh: this
		// is the setting that turns the whole chain of trust off.
		log.Warning(fmt.Sprintf("repository %q is configured with allow-unsigned; its contents are NOT verified", repoKey))
		return nil
	}

	sigData, err := os.ReadFile(sigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s published no %s.\n"+
				"Either sign it (tape-repo sign-repo) or set allow-unsigned = true "+
				"for this repository to accept it unverified",
				ErrUnsigned, repoKey, RepoSigName)
		}
		return err
	}

	sig, err := signing.ParseSignature(sigData)
	if err != nil {
		return fmt.Errorf("signature for %s: %w", repoKey, err)
	}

	keyring, err := signing.LoadKeyring(config.KeysDir())
	if err != nil {
		return fmt.Errorf("loading trusted keys: %w", err)
	}

	index, err := os.Open(indexPath)
	if err != nil {
		return err
	}
	defer index.Close()

	// The signature is bound to the repository name, so a valid signature for
	// some other repository will not pass here.
	if err := signing.Verify(sig, keyring, repoKey, RepoDbName, index); err != nil {
		return fmt.Errorf("verifying %s: %w", repoKey, err)
	}

	log.Info(fmt.Sprintf("repository %q verified against key %s", repoKey, sig.KeyID))
	return nil
}

// VerifyPkgDigest checks a downloaded archive against the digest recorded in
// the index.
//
// The index is signed, so a digest read from it is as trustworthy as the
// signature over it -- which is what makes per-package signatures unnecessary.
func VerifyPkgDigest(archivePath, expectedSha256 string, expectedSize int64, allowUnsigned bool) error {
	log := logger.NewLogger("daemon", "utils.VerifyPkgDigest")

	if expectedSha256 == "" {
		if allowUnsigned {
			log.Warning(fmt.Sprintf("%s has no digest in the index and the repository allows unsigned content; installing unverified", archivePath))
			return nil
		}
		return fmt.Errorf("the index records no digest for this package; the repository needs re-indexing with a current tape-repo")
	}

	info, err := os.Stat(archivePath)
	if err != nil {
		return err
	}
	// Cheap check first: a size mismatch is conclusive without hashing.
	if expectedSize > 0 && info.Size() != expectedSize {
		return fmt.Errorf("package size mismatch: got %d bytes, index says %d", info.Size(), expectedSize)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	digest, err := signing.DigestFile(f)
	if err != nil {
		return err
	}
	if digest != expectedSha256 {
		return fmt.Errorf("package digest mismatch: got %s, index says %s", digest, expectedSha256)
	}

	return nil
}
