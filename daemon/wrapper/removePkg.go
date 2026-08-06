package wrapper

import (
	"encoding/gob"
	"tape/common/database"
	"tape/common/enums"
	"tape/common/global"
	"tape/common/logger"
	"tape/common/structs"
	commonUtils "tape/common/utils"
	"tape/daemon/utils"
)

// RemovePkg uninstalls a package using its recorded file manifest.
func RemovePkg(request *structs.Request, enc *gob.Encoder) error {
	log := logger.NewLogger("daemon", "wrapper.RemovePkg")

	pkgName, err := request.StringData()
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}
	if err := commonUtils.ValidateName(pkgName); err != nil {
		log.VerboseError(err.Error())
		return err
	}

	cfg := global.GetConfig()
	db, err := database.OpenInstalledDB(cfg.GetString("daemon.installed-db"))
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}
	defer db.Close()

	if err := enc.Encode(structs.Response{Type: enums.ResponseTypeStart, Data: pkgName}); err != nil {
		log.VerboseError(err.Error())
		return err
	}

	result, err := utils.RemovePkg(pkgName, utils.RemoveOptions{
		Sysroot: cfg.GetString("daemon.sysroot"),
		Force:   request.BoolOptionOr("force", false),
	}, db)
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	// Report the package, how much was removed, and anything now orphaned, so
	// the CLI can suggest a follow-up cleanup.
	summary := map[string]string{
		"name":    result.Name,
		"files":   itoa(result.FilesRemoved),
		"orphans": joinNames(result.Orphans),
	}

	return enc.Encode(structs.Response{Type: enums.ResponseTypeDone, Data: summary})
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ","
		}
		out += n
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf []byte
	for i > 0 {
		buf = append([]byte{byte('0' + i%10)}, buf...)
		i /= 10
	}
	if neg {
		return "-" + string(buf)
	}
	return string(buf)
}
