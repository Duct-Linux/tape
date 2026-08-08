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
	//
	// It records what each package resolved to, not merely that it was seen.
	// Skipping on the name alone silently honoured whichever constraint the
	// walk happened to reach first: with A -> B -> C >= 2.0 and A -> D -> C
	// >= 3.0, C resolved to 2.x and D's requirement was dropped without a
	// word, producing an install that cannot work.
	visited := make(map[string]*resolution)
	pkg, deps, err := queryPkg(pkgName, resolveDependencies, versionConstrain, visited, 0)
	if err != nil {
		return nil, nil, err
	}
	return pkg, finalDeps(deps, visited), nil
}

// finalDeps collapses the traversal's accumulated entries to one per package,
// taking the version recorded in visited as authoritative and keeping the order
// in which each package was first reached -- dependencies still precede the
// packages that need them.
func finalDeps(deps []map[string]string, visited map[string]*resolution) []map[string]string {
	if len(deps) == 0 {
		return deps
	}
	seen := make(map[string]struct{}, len(deps))
	out := make([]map[string]string, 0, len(deps))
	for _, d := range deps {
		name := d["name"]
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		if r := visited[name]; r != nil && r.pkg != nil {
			out = append(out, r.pkg)
			continue
		}
		out = append(out, d)
	}
	return out
}

// resolution is the version chosen for a package and the constraint it was
// chosen under, so a later requirement can be checked against it.
type resolution struct {
	constraint string
	version    string
	// pkg is the final answer for this package. The traversal accumulates
	// entries as it goes, so re-resolving under a tightened constraint would
	// otherwise leave the superseded entry in the list and the caller would
	// install the version that was just rejected.
	pkg map[string]string
}

// combineConstraints intersects two semver constraints. The library reads a
// comma-separated list as a conjunction, so "> =2.0, <3.0" means both.
func combineConstraints(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	case a == b:
		return a
	}
	return a + ", " + b
}

// satisfies reports whether an already-chosen version meets a constraint. An
// unparseable version or constraint counts as not satisfied, so the resolver
// re-resolves under both rather than assuming the first pick was fine.
func satisfies(version, constraint string) bool {
	if constraint == "" {
		return true
	}
	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return false
	}
	v, err := semver.NewVersion(version)
	if err != nil {
		return false
	}
	return c.Check(v)
}

func queryPkg(
	pkgName string,
	resolveDependencies bool,
	versionConstrain string,
	visited map[string]*resolution,
	depth int,
) (map[string]string, []map[string]string, error) {
	log := logger.NewLogger("daemon", "utils.QueryPkg")

	if depth > maxDependencyDepth {
		return nil, nil, fmt.Errorf("dependency chain for %q exceeds the maximum depth of %d", pkgName, maxDependencyDepth)
	}
	// Provisional: replaced with the chosen version once this package resolves.
	if visited[pkgName] == nil {
		visited[pkgName] = &resolution{constraint: versionConstrain}
	}

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
				if seen := visited[depName]; seen != nil {
					// The version already chosen may not meet this path's
					// requirement. Skipping on the name alone is what silently
					// dropped the second constraint in a diamond.
					if seen.version == "" || satisfies(seen.version, depVersionConstrain) {
						log.VerboseInfo("skipping already-resolved dependency " + depName)
						continue
					}

					combined := combineConstraints(seen.constraint, depVersionConstrain)
					if combined == seen.constraint {
						continue // nothing new to ask for; a cycle would loop here
					}
					log.VerboseInfo(fmt.Sprintf(
						"%s resolved to %s under %q but %s requires %q; re-resolving under both",
						depName, seen.version, seen.constraint, pkgName, depVersionConstrain))
					previous := seen.constraint
					seen.constraint = combined
					depVersionConstrain = combined

					dep, depsOfDep, err := queryPkg(depName, true, combined, visited, depth+1)
					if err != nil {
						log.VerboseError(err.Error())
						return nil, nil, err
					}
					// Contradictory requirements. Reporting this is the whole
					// point: the alternative is installing a version that one
					// of the two dependants cannot use.
					if dep == nil || dep["error"] != "" {
						return nil, nil, fmt.Errorf(
							"cannot satisfy %s: %s requires %q, but %q is required elsewhere",
							depName, pkgName, depVersionConstrain, previous)
					}
					resolvedDeps = append(resolvedDeps, depsOfDep...)
					resolvedDeps = append(resolvedDeps, dep)
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

	// Record the version actually chosen so a later path can check its own
	// constraint against it rather than assuming this one was good enough.
	if r := visited[pkgName]; r != nil {
		r.version = pkg["version"]
		r.constraint = combineConstraints(r.constraint, versionConstrain)
		r.pkg = pkg
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
