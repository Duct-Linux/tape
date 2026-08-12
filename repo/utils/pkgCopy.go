package utils

import (
	"fmt"
	"io"
	"os"
	"path"
	commonUtils "tape/common/utils"

	"github.com/spf13/viper"
)

// PkgCopy publishes a package into the repository and returns the path of the
// archive it wrote.
//
// IT COPIES THE INPUT BYTES. It used to re-tar the extracted contents, and
// that is how every published package lost its setuid, setgid and sticky bits:
// PkgOpen extracts with tarUtils.DefaultUntarOptions, whose PreserveSetuid is
// false, so sanitizeMode() masked all three away -- and the re-tar then wrote
// the sanitised tree out as the artefact clients download. Measured 2026-08-12
// across the whole published index: 622 payloads, 251,608 entries, not one
// carrying any of the three, while the same packages built locally in the same
// builder images carried them correctly.
//
// The specific bits are the symptom. The defect was that publishing rebuilt
// the artefact at all, so anything the extractor normalised was silently
// dropped from the thing users install -- modes today, and whatever the
// extractor learns to normalise next. Copying the bytes removes the class:
// what the repository serves is exactly what the builder produced and CI
// uploaded, and the recorded digest is a digest of that same artefact.
//
// The install side has always been the careful one and is worth quoting,
// because the reasoning applies here verbatim and was missing at this end
// (daemon/utils/install.go):
//
//	Setuid is off by default because an arbitrary archive that can set those
//	bits can hand out root. A package is not an arbitrary archive: its digest
//	was checked against an index carrying a valid signature from a trusted
//	key before we got here [...] Dropping the bits instead would silently ship
//	a broken su, mount or ping -- the file is present and executable and
//	simply does not work.
//
// Written to a temporary name in the destination directory and renamed into
// place, so a failure part-way through cannot leave a truncated package where
// a complete one is expected.
func PkgCopy(srcPath string, pkgConfig *viper.Viper, repoPath string) (published string, err error) {
	name := commonUtils.PkgFormatName(
		pkgConfig.GetString("package.name"),
		pkgConfig.GetString("package.version"),
		pkgConfig.GetString("package.subversion"),
		pkgConfig.GetString("package.arch"),
	)
	outDir := path.Join(repoPath, "packages")

	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", err
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer src.Close()

	dst, err := os.CreateTemp(outDir, ".tape-publish-")
	if err != nil {
		return "", err
	}
	tmpPath := dst.Name()

	committed := false
	defer func() {
		if !committed {
			dst.Close()
			os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(dst, src); err != nil {
		return "", fmt.Errorf("copying %s into the repository: %w", path.Base(srcPath), err)
	}
	// Durable before it becomes visible: a publish interrupted here must not
	// leave a package that exists and is short.
	if err := dst.Sync(); err != nil {
		return "", err
	}
	// CreateTemp makes the file 0600. A published package is world-readable by
	// definition -- it is served over HTTP -- and without this a repository
	// directory exported by a plain file server yields 403s.
	if err := dst.Chmod(0644); err != nil {
		return "", err
	}
	if err := dst.Close(); err != nil {
		return "", err
	}

	target := path.Join(outDir, name)
	if err := os.Rename(tmpPath, target); err != nil {
		return "", err
	}
	committed = true

	return target, nil
}
