package wrapper

import (
	"encoding/gob"
	"strings"
	"tape/common/config"
	"tape/common/database"
	"tape/common/enums"
	"tape/common/global"
	"tape/common/logger"
	"tape/common/structs"
	commonUtils "tape/common/utils"
)

// maxSearchResults bounds a response so a one-character query cannot make the
// daemon serialise an entire distribution's index.
const maxSearchResults = 200

// SearchPkgs finds packages whose name contains the query, across every
// enabled repository.
func SearchPkgs(request *structs.Request, enc *gob.Encoder) error {
	log := logger.NewLogger("daemon", "wrapper.SearchPkgs")

	query, err := request.StringData()
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}
	// The query is a substring match, not a path component, but it still ends
	// up in a SQL LIKE and in log output.
	if len(query) > 128 {
		return errQueryTooLong()
	}

	repoMap, err := config.GetAllRepos()
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	installedDB, err := database.OpenInstalledDB(global.GetConfig().GetString("daemon.installed-db"))
	if err != nil {
		// Marking results as installed is a nicety; not being able to is not
		// fatal to a search.
		log.VerboseWarning(err.Error())
		installedDB = nil
	} else {
		defer installedDB.Close()
	}

	seen := map[string]struct{}{}
	out := make([]map[string]string, 0, 16)

	for _, repo := range commonUtils.RepoSort(repoMap) {
		if !repo.GetBool("repo.enabled") {
			continue
		}

		repoDb, err := database.RepoOpenByName(repo.GetString("key"))
		if err != nil {
			// Visible, not verbose-only: an un-refreshed repository is the most
			// likely reason a search comes back empty.
			log.Warning(err.Error())
			continue
		}

		var pkgs []database.RepoModelPkgs
		tx := repoDb.Where("name LIKE ?", "%"+escapeLike(query)+"%").Find(&pkgs)
		if tx.Error != nil {
			log.VerboseWarning(tx.Error.Error())
			continue
		}

		for _, pkg := range pkgs {
			if _, dup := seen[pkg.Name]; dup {
				continue
			}
			seen[pkg.Name] = struct{}{}

			entry := map[string]string{
				"name":       pkg.Name,
				"version":    pkg.Version,
				"subversion": pkg.Subversion,
				"arch":       pkg.Arch,
				"repo":       repo.GetString("key"),
				"installed":  "",
			}
			if installedDB != nil {
				if yes, _ := installedDB.IsInstalled(pkg.Name); yes {
					entry["installed"] = "yes"
				}
			}

			out = append(out, entry)
			if len(out) >= maxSearchResults {
				return enc.Encode(structs.Response{Type: enums.ResponseTypeDone, Data: out})
			}
		}
	}

	return enc.Encode(structs.Response{Type: enums.ResponseTypeDone, Data: out})
}

// escapeLike neutralises the wildcards SQL LIKE would otherwise interpret, so a
// query of "%" does not match everything.
func escapeLike(q string) string {
	r := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return r.Replace(q)
}
