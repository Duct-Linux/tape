package utils

import (
	"os"
	"tape/common/logger"
)

func Cleanup(pkgPath string) {
	log := logger.NewLogger("builder", "utils.Cleanup")

	err := os.RemoveAll(DirWork(pkgPath))
	if err != nil {
		log.VerboseError(err.Error())
		return
	}
	err = os.RemoveAll(DirWrap(pkgPath))
	if err != nil {
		log.VerboseError(err.Error())
		return
	}
}
