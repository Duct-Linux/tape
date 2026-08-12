package utils

import (
	"fmt"
	"tape/common/database"
	"tape/common/logger"
	commonUtils "tape/common/utils"

	"github.com/Masterminds/semver/v3"
)

// UpgradeCandidate is one installed package with a newer build available.
type UpgradeCandidate struct {
	Name string

	CurrentVersion    string
	CurrentSubversion string

	NewVersion    string
	NewSubversion string

	Repo string
	Arch string
}

func (u UpgradeCandidate) String() string {
	return fmt.Sprintf("%s %s-%s -> %s-%s",
		u.Name, u.CurrentVersion, u.CurrentSubversion, u.NewVersion, u.NewSubversion)
}

// FindUpgrades compares every installed package against the repositories and
// returns those with a newer build.
//
// Only the named packages are considered when names is non-empty; otherwise the
// whole system is checked.
func FindUpgrades(names []string, db *database.InstalledDB) ([]UpgradeCandidate, error) {
	log := logger.NewLogger("daemon", "utils.FindUpgrades")

	installed, err := db.List()
	if err != nil {
		return nil, err
	}

	// Folded, so `tape upgrade libx11` matches the installed "libX11" -- the
	// name the user types and the name the manifest recorded need not agree
	// about case. See commonUtils.FoldName.
	wanted := make(map[string]struct{}, len(names))
	for _, n := range names {
		wanted[commonUtils.FoldName(n)] = struct{}{}
	}

	var candidates []UpgradeCandidate

	for _, pkg := range installed {
		if len(wanted) > 0 {
			if _, ok := wanted[commonUtils.FoldName(pkg.Name)]; !ok {
				continue
			}
		}

		// Resolve without dependencies: what is available for this name alone.
		available, _, err := QueryPkg(pkg.Name, false, "")
		if err != nil {
			log.VerboseWarning(fmt.Sprintf("could not query %s: %s", pkg.Name, err))
			continue
		}
		if available == nil || available["error"] != "" {
			log.VerboseInfo(pkg.Name + " is installed but no longer in any repository")
			continue
		}

		newer, err := isNewer(
			available["version"], available["subversion"],
			pkg.Version, pkg.Subversion,
		)
		if err != nil {
			log.VerboseWarning(fmt.Sprintf("comparing versions for %s: %s", pkg.Name, err))
			continue
		}
		if !newer {
			continue
		}

		candidates = append(candidates, UpgradeCandidate{
			Name:              pkg.Name,
			CurrentVersion:    pkg.Version,
			CurrentSubversion: pkg.Subversion,
			NewVersion:        available["version"],
			NewSubversion:     available["subversion"],
			Repo:              available["repo"],
			Arch:              available["arch"],
		})
	}

	// Report anything explicitly asked for that is not installed at all, rather
	// than silently doing nothing.
	if len(wanted) > 0 {
		present := make(map[string]struct{}, len(installed))
		for _, pkg := range installed {
			present[commonUtils.FoldName(pkg.Name)] = struct{}{}
		}
		for name := range wanted {
			if _, ok := present[name]; !ok {
				return nil, fmt.Errorf("%s is not installed", name)
			}
		}
	}

	return candidates, nil
}

// isNewer compares version then subversion, both parsed as semver.
//
// Subversion is the package revision: the same upstream version rebuilt with a
// patch or different flags still needs to be upgradable.
func isNewer(newVersion, newSubversion, curVersion, curSubversion string) (bool, error) {
	newVer, err := semver.NewVersion(newVersion)
	if err != nil {
		return false, fmt.Errorf("repository version %q: %w", newVersion, err)
	}
	curVer, err := semver.NewVersion(curVersion)
	if err != nil {
		return false, fmt.Errorf("installed version %q: %w", curVersion, err)
	}

	if newVer.GreaterThan(curVer) {
		return true, nil
	}
	if newVer.LessThan(curVer) {
		return false, nil
	}

	// Same upstream version: fall through to the package revision.
	newSub, err := semver.NewVersion(newSubversion)
	if err != nil {
		return false, fmt.Errorf("repository subversion %q: %w", newSubversion, err)
	}
	curSub, err := semver.NewVersion(curSubversion)
	if err != nil {
		return false, fmt.Errorf("installed subversion %q: %w", curSubversion, err)
	}

	return newSub.GreaterThan(curSub), nil
}
