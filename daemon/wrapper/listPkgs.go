package wrapper

import (
	"encoding/gob"
	"tape/common/database"
	"tape/common/enums"
	"tape/common/global"
	"tape/common/logger"
	"tape/common/structs"
)

// ListPkgs returns everything currently installed.
func ListPkgs(request *structs.Request, enc *gob.Encoder) error {
	log := logger.NewLogger("daemon", "wrapper.ListPkgs")

	cfg := global.GetConfig()
	db, err := database.OpenInstalledDB(cfg.GetString("daemon.installed-db"))
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}
	defer db.Close()

	pkgs, err := db.List()
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	explicitOnly := request.BoolOptionOr("explicitOnly", false)
	orphansOnly := request.BoolOptionOr("orphansOnly", false)

	var orphanSet map[string]struct{}
	if orphansOnly {
		orphans, err := db.Orphans()
		if err != nil {
			log.VerboseError(err.Error())
			return err
		}
		orphanSet = make(map[string]struct{}, len(orphans))
		for _, name := range orphans {
			orphanSet[name] = struct{}{}
		}
	}

	// The wire format is the same []map[string]string the query path uses, so
	// no new gob types need registering.
	out := make([]map[string]string, 0, len(pkgs))
	for _, pkg := range pkgs {
		if explicitOnly && pkg.Reason != database.ReasonExplicit {
			continue
		}
		if orphansOnly {
			if _, isOrphan := orphanSet[pkg.Name]; !isOrphan {
				continue
			}
		}

		out = append(out, map[string]string{
			"name":        pkg.Name,
			"version":     pkg.Version,
			"subversion":  pkg.Subversion,
			"arch":        pkg.Arch,
			"repo":        pkg.Repo,
			"reason":      string(pkg.Reason),
			"installedAt": pkg.InstalledAt.Format("2006-01-02 15:04:05"),
		})
	}

	return enc.Encode(structs.Response{Type: enums.ResponseTypeDone, Data: out})
}
