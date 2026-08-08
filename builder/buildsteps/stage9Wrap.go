package buildsteps

import (
	"path"
	"tape/builder/utils"
	"tape/common/logger"
	commonUtils "tape/common/utils"

	cp "github.com/otiai10/copy"
	"github.com/spf13/viper"
)

func Stage9Wrap() error {
	log := logger.NewLogger("builder", "buildsteps.Stage9Wrap")

	// Copy the install files to the package install directory
	err := cp.Copy(utils.DirWorkInstall(pkgPath), utils.DirWrapInstall(pkgPath))
	if err != nil {
		log.VerboseError("Failed to copy install files to package install directory")
		log.VerboseError(err.Error())
		return err
	}

	// set vars
	var (
		pkgName   = buildConfig.GetString("package.name")
		pkgDesc   = buildConfig.GetString("package.description")
		pkgVer    = buildConfig.GetString("package.version")
		pkgSubVer = buildConfig.GetString("package.subversion")
		// The target architecture, not the host's. --target used to affect only
		// the TAPE_TARGET environment variable, so every cross-built package was
		// stamped with the architecture of the machine that built it -- and then
		// refused, or worse accepted, on the machine it was meant for.
		pkgArch = targetArch
	)

	// build TAPEPACKAGE.toml
	wrapConfig := viper.New()

	wrapConfig.SetConfigFile(path.Join(utils.DirWrap(pkgPath), "TAPEPACKAGE.toml"))

	wrapConfig.Set("package.name", pkgName)
	wrapConfig.Set("package.description", pkgDesc)
	wrapConfig.Set("package.version", pkgVer)
	wrapConfig.Set("package.subversion", pkgSubVer)
	wrapConfig.Set("package.authors", buildConfig.GetStringSlice("package.authors"))
	wrapConfig.Set("package.packagers", buildConfig.GetStringSlice("package.packagers"))
	wrapConfig.Set("package.arch", pkgArch)

	deps := buildConfig.GetStringMap("dependencies")

	// What the payload actually links against, against what the recipe claims.
	// A package can otherwise install cleanly and fail to run.
	checkDeclaredDeps(utils.DirWrapInstall(pkgPath), deps, log)

	// filter out build dependencies
	deps["build"] = nil
	wrapConfig.Set("dependencies", deps)

	err = wrapConfig.WriteConfig()
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	// Build a tar. This error used to be discarded, so a failed or misdirected
	// wrap still reported "Done!" and left the user with no package.
	if err := commonUtils.PkgBuildTar(
		commonUtils.PkgFormatName(pkgName, pkgVer, pkgSubVer, pkgArch),
		utils.DirWrap(pkgPath),
		out,
	); err != nil {
		log.VerboseError(err.Error())
		return err
	}

	return nil
}
