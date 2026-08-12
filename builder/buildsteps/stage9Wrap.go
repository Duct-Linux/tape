package buildsteps

import (
	"path"
	"tape/builder/utils"
	"tape/common/logger"
	"tape/common/manifest"
	commonUtils "tape/common/utils"

	cp "github.com/otiai10/copy"
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

	// The dependency table is read from the recipe file directly rather than
	// through buildConfig, because a [dependencies] key is a package NAME and
	// viper lower-cases every key it returns: "libXau = ..." came back as
	// "libxau", and that is what got written into the manifest and published.
	// package.name above is a VALUE, which is why it was never affected and why
	// the index ended up holding a package called libXau that nothing could ask
	// for by name. See tape/common/manifest.
	//
	// The resolver's case-insensitive matching is what keeps every manifest
	// published before this fix installable; this only stops new ones being
	// written wrong. Do not tighten one without the other.
	deps, err := manifest.ReadDependencies(utils.PkgBuildConfigPath(pkgPath))
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	// What the payload actually links against, against what the recipe claims.
	// A package can otherwise install cleanly and fail to run.
	checkDeclaredDeps(utils.DirWrapInstall(pkgPath), deps.Runtime, log)

	// Build dependencies are not carried into the package: deps.Build is simply
	// not written. The old builder set the key to nil instead, which viper
	// omitted from the file, so the result on disk is the same.
	if err := manifest.WritePackage(
		path.Join(utils.DirWrap(pkgPath), "TAPEPACKAGE.toml"),
		manifest.PackageManifest{
			Package: manifest.Package{
				Name:        pkgName,
				Description: pkgDesc,
				Version:     pkgVer,
				Subversion:  pkgSubVer,
				Authors:     buildConfig.GetStringSlice("package.authors"),
				Packagers:   buildConfig.GetStringSlice("package.packagers"),
				Arch:        pkgArch,
			},
			Dependencies: deps.Runtime,
		},
	); err != nil {
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
