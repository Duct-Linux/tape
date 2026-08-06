package utils

import (
	"errors"
	"fmt"
	"tape/common/arch"
	"tape/common/config"
	"tape/common/database"
	"tape/common/logger"
	"tape/common/utils"

	"github.com/Masterminds/semver/v3"
	"github.com/spf13/viper"
)

// maxDependencyDepth bounds the resolver. Combined with the visited set it
// makes a malformed repository unable to wedge the daemon.
const maxDependencyDepth = 64

/**
 * QueryPkg
 * Query package from all repos
 *
 * @param {string} pkgName
 * @param {bool} resolveDependencies
 * @param {string} versionConstrain
 * @return {map[string]string} pkgData
 * @return {[]map[string]string} dependencies
 * @return {error} err
 */
func QueryPkg(pkgName string, resolveDependencies bool, versionConstrain string) (map[string]string, []map[string]string, error) {
	// visited is keyed by package name and shared across the whole traversal.
	// Without it, a dependency cycle (A -> B -> A) recursed until the stack
	// overflowed -- which kills the process outright, ignoring any recover --
	// and a diamond dependency produced exponential duplicate work.
	visited := make(map[string]struct{})
	return queryPkg(pkgName, resolveDependencies, versionConstrain, visited, 0)
}

func queryPkg(
	pkgName string,
	resolveDependencies bool,
	versionConstrain string,
	visited map[string]struct{},
	depth int,
) (map[string]string, []map[string]string, error) {
	log := logger.NewLogger("daemon", "utils.QueryPkg")

	if depth > maxDependencyDepth {
		return nil, nil, fmt.Errorf("dependency chain for %q exceeds the maximum depth of %d", pkgName, maxDependencyDepth)
	}
	visited[pkgName] = struct{}{}

	repoMap, err := config.GetAllRepos()
	if err != nil {
		log.VerboseError(err.Error())
		return nil, nil, err
	}

	repos := utils.RepoSort(repoMap)

	var pkg map[string]string
	var resolvedDeps []map[string]string

	for _, repo := range repos {
		if !repo.GetBool("repo.enabled") {
			continue
		}

		var deps map[string]string
		pkg, deps, err = queryPkgFromRepo(repo, pkgName, resolveDependencies, versionConstrain)
		if err != nil {
			log.VerboseError(err.Error())
			return nil, nil, err
		}

		if pkg == nil {
			continue
		}

		if resolveDependencies && deps != nil {
			log.VerboseInfo("resolving dependencies for " + pkgName)
			for depName, depVersionConstrain := range deps {
				// Already reached through another path (or the cycle closed).
				if _, seen := visited[depName]; seen {
					log.VerboseInfo("skipping already-resolved dependency " + depName)
					continue
				}

				dep, depsOfDep, err := queryPkg(depName, true, depVersionConstrain, visited, depth+1)
				if err != nil {
					log.VerboseError(err.Error())
					return nil, nil, err
				}

				resolvedDeps = append(resolvedDeps, depsOfDep...)
				if dep != nil {
					resolvedDeps = append(resolvedDeps, dep)
				}
			}
		}

		break
	}

	if pkg == nil {
		log.VerboseError("package " + pkgName + " not found in any repo")
		return queryPkgResponseError(pkgName, "package not found"), nil, nil
	}

	return pkg, resolvedDeps, nil
}

func queryPkgFromRepo(repo *viper.Viper, pkgName string, resolveDependencies bool, versionConstrain string) (map[string]string, map[string]string, error) {
	log := logger.NewLogger("daemon", "utils.QueryPkgFromRepo")

	repoDb, err := database.RepoOpenByName(repo.GetString("key"))
	if err != nil {
		if errors.Is(err, database.ErrRepoNotCached) {
			// Not an error for the query as a whole: the other repositories may
			// still have the package. refresh-repos will populate this one.
			log.Warning(repo.GetString("key") + " has not been refreshed yet; skipping")
			return nil, nil, nil
		}
		log.VerboseError(err.Error())
		return nil, nil, err
	}

	var pkg []database.RepoModelPkgs
	tx := repoDb.Find(&pkg, "name = ?", pkgName)
	if tx.Error != nil {
		log.VerboseError(tx.Error.Error())
		return nil, nil, tx.Error
	}

	if len(pkg) == 0 {
		log.VerboseInfo("package " + pkgName + " not found in " + repo.GetString("key"))
		return nil, nil, nil
	}

	// Drop builds for other architectures before anything else looks at them.
	// Without this the resolver matched on name alone, so an aarch64 machine
	// would resolve, download and install an x86_64 package.
	sysArch := arch.Current()
	compatible := pkg[:0:0]
	for _, p := range pkg {
		if arch.Compatible(p.Arch, sysArch) {
			compatible = append(compatible, p)
			continue
		}
		log.VerboseInfo(fmt.Sprintf("skipping %s %s-%s: built for %s, this system is %s",
			p.Name, p.Version, p.Subversion, p.Arch, sysArch))
	}
	if len(compatible) == 0 {
		log.VerboseInfo(fmt.Sprintf("%s exists in %s but not for %s",
			pkgName, repo.GetString("key"), sysArch))
		return nil, nil, nil
	}
	pkg = compatible

	// Version constraints.
	var pkgs []database.RepoModelPkgs
	if versionConstrain != "" {
		c, err := semver.NewConstraint(versionConstrain)
		if err != nil {
			log.VerboseError(err.Error())
			return nil, nil, err
		}

		for _, p := range pkg {
			// semver.MustParse panics, and Version is a free-form string
			// populated from a package's TOML with no validation -- one entry
			// reading "alpha" used to kill the daemon on every query that
			// touched this repo.
			v, err := semver.NewVersion(p.Version)
			if err != nil {
				log.VerboseWarning(fmt.Sprintf("skipping %s: unparseable version %q: %s", p.Name, p.Version, err))
				continue
			}

			if !c.Check(v) {
				log.VerboseInfo("version " + p.Version + " doesn't match the constraint")
				continue
			}

			pkgs = append(pkgs, p)
		}
	} else {
		pkgs = pkg
	}

	if len(pkgs) == 0 {
		log.VerboseInfo("no matching package version found")
		return nil, nil, nil
	}

	latestPkg, ok := selectLatest(pkgs, log)
	if !ok {
		return nil, nil, nil
	}

	pkgData := queryPkgResponsePkg(repo.GetString("key"), latestPkg.Name, latestPkg.Version, latestPkg.Subversion, latestPkg.Arch)

	var dependencies map[string]string
	if resolveDependencies {
		var dep []database.RepoModelDependencies
		tx = repoDb.Where("pkg_id = ?", latestPkg.ID).Find(&dep)
		if tx.Error != nil {
			log.VerboseError(tx.Error.Error())
			return nil, nil, tx.Error
		}
		if len(dep) > 0 {
			dependencies = make(map[string]string)
			for _, d := range dep {
				dependencies[d.Name] = d.VersionConstraint
			}
		}
	}

	return pkgData, dependencies, nil
}

// selectLatest picks the highest version, breaking ties on subversion. Entries
// whose version or subversion will not parse are skipped rather than fatal.
func selectLatest(pkgs []database.RepoModelPkgs, log *logger.Logger) (database.RepoModelPkgs, bool) {
	var (
		latestVersion    *semver.Version
		latestSubversion *semver.Version
		latestPkg        database.RepoModelPkgs
		found            bool
	)

	for _, p := range pkgs {
		ver, err := semver.NewVersion(p.Version)
		if err != nil {
			log.VerboseWarning(fmt.Sprintf("skipping %s: unparseable version %q", p.Name, p.Version))
			continue
		}
		subver, err := semver.NewVersion(p.Subversion)
		if err != nil {
			log.VerboseWarning(fmt.Sprintf("skipping %s: unparseable subversion %q", p.Name, p.Subversion))
			continue
		}

		switch {
		case !found, ver.GreaterThan(latestVersion):
			latestVersion, latestSubversion, latestPkg, found = ver, subver, p, true
		case ver.Equal(latestVersion) && subver.GreaterThan(latestSubversion):
			latestSubversion, latestPkg = subver, p
		}
	}

	return latestPkg, found
}

func queryPkgResponseError(pkgName string, error string) map[string]string {
	pkgData := make(map[string]string)
	pkgData["name"] = pkgName
	pkgData["error"] = error

	return pkgData
}

func queryPkgResponsePkg(repo, name, version, subversion, arch string) map[string]string {
	pkgData := make(map[string]string)
	pkgData["name"] = name
	pkgData["version"] = version
	pkgData["subversion"] = subversion
	pkgData["arch"] = arch
	pkgData["repo"] = repo

	return pkgData
}
