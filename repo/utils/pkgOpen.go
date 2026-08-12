package utils

import (
	"os"
	"path"
	"tape/common/logger"
	"tape/common/tarUtils"

	"github.com/spf13/viper"
)

func PkgOpen(pkgPath string) (string, *viper.Viper, error) {
	log := logger.NewLogger("repo", "utils.PkgOpen")

	// This extraction exists ONLY to read TAPEPACKAGE.toml. Nothing published
	// comes from it: PkgCopy copies the input bytes, so the tree written here
	// is read once and thrown away.
	//
	// That is why PreserveSetuid stays at its default false below. It is the
	// right default for the operation this now is -- writing a stranger's
	// archive into a temporary directory as root -- and no longer costs
	// anything, because the sanitised tree is not what gets served. Turning it
	// on here would recreate the privilege-escalation shape without buying
	// back a single bit.
	//
	// os.MkdirTemp rather than a math/rand name under a shared 0755 parent: it
	// uses crypto-grade randomness and creates the directory exclusively at
	// 0700, so another user on the publish host cannot pre-create or read it.
	// The daemon's download path was already fixed this way; this end was not.
	parent := path.Join(os.TempDir(), "tape")
	if err := os.MkdirAll(parent, 0755); err != nil {
		log.VerboseError(err.Error())
		return "", nil, err
	}
	tmpDir, err := os.MkdirTemp(parent, "open-")
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
