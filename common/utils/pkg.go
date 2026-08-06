package utils

import (
	"fmt"
	"os"
	"path"
	"tape/common/logger"
	"tape/common/tarUtils"
)

func PkgBuildTar(pkgName string, wrapDir string, out string) error {
	log := logger.NewLogger("common", "utils.PkgBuildTar")

	pkgTarPath := path.Join(out, pkgName)

	// make sure the package base dir exists
	err := os.MkdirAll(out, os.ModePerm)
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	pkgTarFile, err := os.Create(pkgTarPath)
	if err != nil {
		log.VerboseError("Failed to create package file")
		log.VerboseError(err.Error())
		return err
	}
	defer pkgTarFile.Close()
	err = tarUtils.Tar(wrapDir, pkgTarFile)
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}
	return nil
}

func PkgFormatName(name string, version string, subversion string, arch string) string {
	return fmt.Sprintf("%s-%s-%s.%s.tape.tar.gz", name, version, subversion, arch)
}
