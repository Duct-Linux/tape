package wrapper

import (
	"encoding/gob"
	"tape/common/enums"
	"tape/common/logger"
	"tape/common/structs"
	commonUtils "tape/common/utils"
	"tape/daemon/utils"
)

func QueryPkg(request *structs.Request, enc *gob.Encoder) error {
	log := logger.NewLogger("daemon", "wrapper.QueryPkg")

	pkgName, err := request.StringData()
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}
	if err := commonUtils.ValidateName(pkgName); err != nil {
		log.VerboseError(err.Error())
		return err
	}

	// A missing or malformed option degrades to the default instead of
	// panicking the daemon on a nil Options map.
	resolveDependencies := request.BoolOptionOr("resolveDependencies", false)

	err = enc.Encode(structs.Response{Type: enums.ResponseTypeStart})
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	pkgData, dependencies, err := utils.QueryPkg(pkgName, resolveDependencies, "")
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	// Copy rather than append onto the resolver's slice: appending to
	// `dependencies` in place can write into a backing array the caller still
	// holds a view of.
	data := make([]map[string]string, 0, len(dependencies)+1)
	data = append(data, dependencies...)
	data = append(data, pkgData)

	err = enc.Encode(structs.Response{Type: enums.ResponseTypeDone, Data: data})
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	return nil
}
