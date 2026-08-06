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

// CheckUpgrades reports which installed packages have a newer build available.
//
// It only reports; the CLI drives the actual download and install through the
// existing paths, so an upgrade is an ordinary install of a newer archive.
func CheckUpgrades(request *structs.Request, enc *gob.Encoder) error {
	log := logger.NewLogger("daemon", "wrapper.CheckUpgrades")

	// Data is an optional list of package names to limit the check to.
	var names []string
	if request.Data != nil {
		list, ok := request.Data.([]string)
		if !ok {
			return errNotAStringSlice(request.Data)
		}
		for _, name := range list {
			if err := commonUtils.ValidateName(name); err != nil {
				log.VerboseError(err.Error())
				return err
			}
		}
		names = list
	}

	cfg := global.GetConfig()
	db, err := database.OpenInstalledDB(cfg.GetString("daemon.installed-db"))
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}
	defer db.Close()

	candidates, err := utils.FindUpgrades(names, db)
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	out := make([]map[string]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, map[string]string{
			"name":              c.Name,
			"version":           c.NewVersion,
			"subversion":        c.NewSubversion,
			"arch":              c.Arch,
			"repo":              c.Repo,
			"currentVersion":    c.CurrentVersion,
			"currentSubversion": c.CurrentSubversion,
		})
	}

	return enc.Encode(structs.Response{Type: enums.ResponseTypeDone, Data: out})
}
