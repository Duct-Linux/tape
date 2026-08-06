package utils

import (
	"path"
	commonUtils "tape/common/utils"

	"github.com/spf13/viper"
)

// PkgCopy publishes a package into the repository and returns the path of the
// archive it wrote.
//
// Note this re-tars the extracted contents rather than copying the input file:
// the published archive is not byte-identical to the one handed to
// add-to-repo, which is why callers must digest the returned path.
func PkgCopy(tmpDir string, pkgConfig *viper.Viper, repoPath string) (string, error) {
	name := commonUtils.PkgFormatName(
		pkgConfig.GetString("package.name"),
		pkgConfig.GetString("package.version"),
		pkgConfig.GetString("package.subversion"),
		pkgConfig.GetString("package.arch"),
	)
	outDir := path.Join(repoPath, "packages")

	if err := commonUtils.PkgBuildTar(name, tmpDir, outDir); err != nil {
		return "", err
	}
	return path.Join(outDir, name), nil
}
