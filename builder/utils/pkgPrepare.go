package utils

import (
	"os"
	"tape/common/logger"
)

func PkgPrepare(pkgPath string) error {
	log := logger.NewLogger("builder", "PkgPrepare")
	log.VerboseInfo("Preparing package")

	err := os.MkdirAll(DirWork(pkgPath), 0755)
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	err = os.MkdirAll(DirWorkInstall(pkgPath), 0755)
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	err = os.MkdirAll(DirWrap(pkgPath), 0755)
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	err = os.MkdirAll(DirWrapInstall(pkgPath), 0755)
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	return nil
}
