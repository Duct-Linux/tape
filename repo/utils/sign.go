package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"tape/common/signing"
	"time"
)

// RepoDbName is the index file inside a repository.
const RepoDbName = "repo.db"

// RepoSigName is the detached signature that accompanies it.
const RepoSigName = RepoDbName + ".sig"

// SignRepo writes a detached signature for a repository's index.
//
// The signature is bound to repoName, so it cannot be replayed against a
// different repository even by someone holding a legitimately signed file.
func SignRepo(repoPath, repoName, keyPath string, now time.Time) error {
	priv, err := signing.LoadPrivateKey(keyPath)
	if err != nil {
		return fmt.Errorf("loading signing key: %w", err)
	}

	dbPath := filepath.Join(repoPath, RepoDbName)
	db, err := os.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", dbPath, err)
	}
	defer db.Close()

	sig, err := signing.Sign(priv, repoName, RepoDbName, db, now)
	if err != nil {
		return err
	}

	sigPath := filepath.Join(repoPath, RepoSigName)
	return os.WriteFile(sigPath, sig.Marshal(), 0644)
}

// InvalidateRepoSignature removes a stale signature.
//
// Called after the index changes: leaving the old signature in place would give
// clients a file that fails verification with a confusing "content does not
// match" rather than the accurate "this repository has not been re-signed".
func InvalidateRepoSignature(repoPath string) error {
	err := os.Remove(filepath.Join(repoPath, RepoSigName))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
