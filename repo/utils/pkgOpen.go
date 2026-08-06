package utils

import (
	"os"
	"path"
	"tape/common/logger"
	"tape/common/tarUtils"
	commonUtils "tape/common/utils"

	"github.com/spf13/viper"
)

func PkgOpen(pkgPath string) (string, *viper.Viper, error) {
	log := logger.NewLogger("repo", "utils.PkgOpen")

	// create a temporary directory
	tmpDir := path.Join(os.TempDir(), "tape", commonUtils.RandStringRunes(10))
	err := os.MkdirAll(tmpDir, 0755)
	if err != nil {
		log.VerboseError(err.Error())
		return "", nil, err
	}

	// extract the package
	pkgIo, err := os.Open(pkgPath)
	if err != nil {
		log.VerboseError(err.Error())
		return "", nil, err
	}
	defer pkgIo.Close()

	// Packages legitimately contain symlinks (soname links on shared
	// libraries), so they are permitted here; Untar still confines every link
	// target to tmpDir.
	opts := tarUtils.DefaultUntarOptions
	opts.AllowLinks = true
	if err := tarUtils.UntarWithOptions(tmpDir, pkgIo, opts); err != nil {
		log.VerboseError(err.Error())
		return "", nil, err
	}

	// open the package config
	pkgConfig := viper.New()
	pkgConfig.SetConfigFile(path.Join(tmpDir, "TAPEPACKAGE.toml"))
	err = pkgConfig.ReadInConfig()
	if err != nil {
		log.VerboseError(err.Error())
		return "", nil, err
	}

	return tmpDir, pkgConfig, nil
}
