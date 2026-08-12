package utils

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"tape/common/config"
	"tape/common/database"
	"tape/common/logger"
	"tape/common/structs"
	commonUtils "tape/common/utils"
)

// PkgCacheRoot is where downloaded package archives are staged. It is the only
// directory a client may name when asking the daemon to install something.
func PkgCacheRoot() string {
	return path.Join(os.TempDir(), "tape")
}

// DownloadPkg fetches one package build into a private temporary directory and
// returns its path plus a human-readable size.
//
// ref is already validated (see structs.PkgRefFromMap), which is what keeps the
// repo lookup and the destination path from being steerable by the caller.
func DownloadPkg(ref structs.PkgRef, progress func(int8)) (string, string, error) {
	log := logger.NewLogger("daemon", "utils.DownloadPkg")

	// ref.Repo is a validated single path component, so this cannot escape
	// /etc/tape/repos the way a raw client string could.
	repoConfig, err := config.GetRepoConfigByName(ref.Repo)
	if err != nil {
		log.VerboseError(err.Error())
		return "", "", err
	}

	// Resolve the index row before anything is built from the name. The name in
	// ref may be spelled the way a manifest asked for it rather than the way
	// the repository publishes it -- a manifest written by the old builder says
	// "libxau" for a package the repository calls "libXau" -- and the archive on
	// the server is named with the repository's spelling. Matching the row
	// case-insensitively and then formatting the file name from the row is what
	// makes the two agree; formatting from ref.Name would look up the right row
	// and then fetch a URL that 404s.
	canonicalName, expected, expectedSize, err := pkgFromIndex(ref)
	if err != nil {
		log.VerboseError(err.Error())
		return "", "", err
	}

	baseUrl := repoConfig.GetString("repo.baseurl")
	pkgFileName := commonUtils.PkgFormatName(canonicalName, ref.Version, ref.Subversion, ref.Arch)

	pkgUrl, err := joinPkgLocation(baseUrl, pkgFileName)
	if err != nil {
		log.VerboseError(err.Error())
		return "", "", err
	}

	// os.MkdirTemp uses crypto-grade randomness and creates the directory
	// exclusively (0700), closing the predictable-name TOCTOU that
	// math/rand-derived names left open.
	parent := PkgCacheRoot()
	if err := os.MkdirAll(parent, 0755); err != nil {
		log.VerboseError(err.Error())
		return "", "", err
	}
	tmpDir, err := os.MkdirTemp(parent, "pkg-")
	if err != nil {
		log.VerboseError(err.Error())
		return "", "", err
	}

	pkgTmpPath := filepath.Join(tmpDir, pkgFileName)

	// Belt and braces: even with a validated ref, assert the destination stayed
	// inside the directory we just created before writing to it as root.
	if !strings.HasPrefix(pkgTmpPath, tmpDir+string(os.PathSeparator)) {
		os.RemoveAll(tmpDir)
		return "", "", fmt.Errorf("refusing to write %q outside %q", pkgTmpPath, tmpDir)
	}

	size, err := commonUtils.DownloadFile(pkgUrl, pkgTmpPath, repoConfig.GetBool("repo.skip-tls"), progress)
	if err != nil {
		log.VerboseError(err.Error())
		// Do not leave a partial package behind for a later step to pick up.
		os.RemoveAll(tmpDir)
		return "", "", err
	}

	// Check the archive against the digest recorded in the index. The index is
	// signed, so this extends that signature to cover the package itself --
	// which is why individual archives do not need their own signatures.
	//
	// This happens before the path is ever returned, so an unverified archive
	// is never a candidate for installation. The digest came from the same row
	// the file name did, read once above.
	if err := VerifyPkgDigest(pkgTmpPath, expected, expectedSize, repoConfig.GetBool("repo.allow-unsigned")); err != nil {
		log.Error(fmt.Sprintf("refusing %s: %s", ref, err))
		os.RemoveAll(tmpDir)
		return "", "", fmt.Errorf("verifying %s: %w", ref.Name, err)
	}

	return pkgTmpPath, commonUtils.ConvertBytesToHumanReadable(size), nil
}

// pkgFromIndex looks up a package build in the repository index and returns the
// name as the repository spells it, plus the digest and size it records.
//
// The name is matched with NOCASE, the same collation the resolver uses: see
// utils.FoldName for why the two spellings exist at all.
func pkgFromIndex(ref structs.PkgRef) (string, string, int64, error) {
	repoDb, err := database.RepoOpenByName(ref.Repo)
	if err != nil {
		return "", "", 0, err
	}

	var row database.RepoModelPkgs
	tx := repoDb.Where(
		"name = ? COLLATE NOCASE AND version = ? AND subversion = ? AND arch = ?",
		ref.Name, ref.Version, ref.Subversion, ref.Arch,
	).First(&row)
	if tx.Error != nil {
		return "", "", 0, fmt.Errorf("%s is not listed in the %s index", ref.Name, ref.Repo)
	}

	return row.Name, row.Sha256, row.Size, nil
}

// joinPkgLocation builds the package location for either a remote baseurl or a
// local directory baseurl (the dev repos use "/tmp/..."), which url.JoinPath
// mangles into a relative URL.
func joinPkgLocation(baseUrl, pkgFileName string) (string, error) {
	if baseUrl == "" {
		return "", fmt.Errorf("repo has no baseurl configured")
	}
	if strings.HasPrefix(baseUrl, "/") {
		return path.Join(baseUrl, "packages", pkgFileName), nil
	}
	return url.JoinPath(baseUrl, "packages", pkgFileName)
}
