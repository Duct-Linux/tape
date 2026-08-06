package utils

import (
	"os"
	"tape/common/signing"
)

// PkgDigest returns the sha256 and size of a package archive as published.
func PkgDigest(pkgPath string) (string, int64, error) {
	info, err := os.Stat(pkgPath)
	if err != nil {
		return "", 0, err
	}

	f, err := os.Open(pkgPath)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	digest, err := signing.DigestFile(f)
	if err != nil {
		return "", 0, err
	}

	return digest, info.Size(), nil
}
