package wrapper

import (
	"encoding/gob"
	"fmt"
	"path/filepath"
	"strings"
	"tape/common/database"
	"tape/common/enums"
	"tape/common/global"
	"tape/common/logger"
	"tape/common/structs"
	commonUtils "tape/common/utils"
	"tape/daemon/utils"
)

// LocalInstall installs an already-downloaded package archive.
//
// The client supplies a path, so it is constrained to the daemon's own cache
// directory: an unauthenticated caller must not be able to point the root
// daemon at an arbitrary archive of their own making.
func LocalInstall(request *structs.Request, enc *gob.Encoder) error {
	log := logger.NewLogger("daemon", "wrapper.LocalInstall")

	archivePath, err := request.StringData()
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	cleanPath, err := validateCachePath(archivePath)
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	reason := database.ReasonExplicit
	if request.BoolOptionOr("asDependency", false) {
		reason = database.ReasonDependency
	}

	// Which repo the package came from is known at download time, not from the
	// archive: TAPEPACKAGE.toml has no repo field. Without it the installed
	// list has an empty Repository column and a future upgrade has nowhere to
	// look for a newer build.
	origin := request.StringOptionOr("repo", "")
	if origin != "" {
		if err := commonUtils.ValidateName(origin); err != nil {
			log.VerboseError(err.Error())
			return err
		}
	}

	cfg := global.GetConfig()
	db, err := database.OpenInstalledDB(cfg.GetString("daemon.installed-db"))
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}
	defer db.Close()

	if err := enc.Encode(structs.Response{
		Type: enums.ResponseTypeStart,
		Data: filepath.Base(cleanPath),
	}); err != nil {
		log.VerboseError(err.Error())
		return err
	}

	installOpts := utils.InstallOptionsFromConfig(cfg, reason)
	installOpts.Repo = origin

	pkg, err := utils.InstallPkg(
		cleanPath,
		installOpts,
		db,
		utils.UpdateProgress(enc),
	)
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	if err := enc.Encode(structs.Response{
		Type: enums.ResponseTypeProgressDone,
		Data: []string{pkg.Name, pkg.Version + "-" + pkg.Subversion},
	}); err != nil {
		log.VerboseError(err.Error())
		return err
	}

	return enc.Encode(structs.Response{Type: enums.ResponseTypeDone, Data: pkg.Name})
}

// validateCachePath confines an install to archives the daemon itself
// downloaded. DownloadPkg stages into <tmp>/tape/pkg-*, and that is the only
// place a client may name.
func validateCachePath(archivePath string) (string, error) {
	if archivePath == "" {
		return "", fmt.Errorf("no package path given")
	}

	clean := filepath.Clean(archivePath)
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("package path must be absolute")
	}

	cacheRoot := utils.PkgCacheRoot()
	if clean != cacheRoot && !strings.HasPrefix(clean, cacheRoot+"/") {
		return "", fmt.Errorf("package path must be inside %s", cacheRoot)
	}
	if !strings.HasSuffix(clean, ".tape.tar.gz") {
		return "", fmt.Errorf("not a tape package archive: %s", filepath.Base(clean))
	}

	return clean, nil
}
