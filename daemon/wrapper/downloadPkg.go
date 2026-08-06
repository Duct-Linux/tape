package wrapper

import (
	"encoding/gob"
	"tape/common/enums"
	"tape/common/logger"
	"tape/common/structs"
	"tape/daemon/utils"
)

func DownloadPkg(request *structs.Request, enc *gob.Encoder) error {
	log := logger.NewLogger("daemon", "wrapper.DownloadPkg")

	resData, err := request.StringMapData()
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	// Validate before any of these five strings reaches a path or a URL.
	ref, err := structs.PkgRefFromMap(resData)
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	err = enc.Encode(structs.Response{Type: enums.ResponseTypeStart, Data: ref.Name})
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	cachePath, size, err := utils.DownloadPkg(ref, utils.UpdateProgress(enc))
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	err = enc.Encode(structs.Response{Type: enums.ResponseTypeProgressDone, Data: []string{ref.Name, size}})
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	err = enc.Encode(structs.Response{Type: enums.ResponseTypeDone, Data: cachePath})
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	return nil
}
