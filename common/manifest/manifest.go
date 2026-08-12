// Package manifest reads and writes TAPEBUILD.toml and TAPEPACKAGE.toml
// without going through viper.
//
// viper is used everywhere else in tape and is fine for reading VALUES, but a
// [dependencies] key is a package NAME, and viper lower-cases every key it
// returns and every key it writes -- GetStringMap, Sub().AllSettings() and
// WriteConfig() all do it, unconditionally, with no option to turn it off. That
// is how every dependency name in every package published to date came to be
// lower-cased while the package it names kept its own case: package.name is a
// value and survives, "libXau = ..." is a key and does not.
//
// So the manifest's dependency table is parsed and emitted here with go-toml
// directly. Anything reading a plain field may still use viper.
//
// This stops NEW manifests being written wrong. It repairs nothing already
// published: the index is full of lower-cased names, and what keeps those
// installable is the resolver matching names case-insensitively (see
// utils.FoldName). The two are a pair and must stay compatible -- a resolver
// made strict again on the grounds that the builder now gets it right would
// break every package published before today.
package manifest

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// BuildDepsKey is the sub-table of [dependencies] holding build-time-only
// dependencies. It is a reserved name, not a package.
const BuildDepsKey = "build"

// Package is the [package] table of a TAPEPACKAGE.toml.
type Package struct {
	Name        string   `toml:"name"`
	Description string   `toml:"description"`
	Version     string   `toml:"version"`
	Subversion  string   `toml:"subversion"`
	Authors     []string `toml:"authors"`
	Packagers   []string `toml:"packagers"`
	Arch        string   `toml:"arch"`
}

// PackageManifest is a whole TAPEPACKAGE.toml.
//
// The build sub-table is deliberately absent: a built package's manifest lists
// only what it needs at runtime.
type PackageManifest struct {
	Package      Package           `toml:"package"`
	Dependencies map[string]string `toml:"dependencies"`
}

// Dependencies is a recipe's or a package's dependency table, with the names
// spelled as the author wrote them.
type Dependencies struct {
	// Runtime maps a package name to its version constraint ("" for none).
	Runtime map[string]string
	// Build is the [dependencies.build] sub-table, empty for a built package.
	Build map[string]string
}

// ReadDependencies parses the [dependencies] table of a TOML file.
//
// A missing table is not an error: a package with no dependencies is ordinary,
// and both maps come back empty. A non-string entry IS an error -- it is a
// malformed recipe, and a dependency silently dropped here becomes a package
// that installs and cannot run.
func ReadDependencies(path string) (Dependencies, error) {
	deps := Dependencies{
		Runtime: map[string]string{},
		Build:   map[string]string{},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return deps, err
	}

	var doc struct {
		Dependencies map[string]any `toml:"dependencies"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return deps, fmt.Errorf("parsing %s: %w", path, err)
	}

	for name, raw := range doc.Dependencies {
		if name == BuildDepsKey {
			// A nil value is what the old builder wrote to mean "no build
			// dependencies", so it is not malformed -- see WritePackage.
			if raw == nil {
				continue
			}
			sub, ok := raw.(map[string]any)
			if !ok {
				return deps, fmt.Errorf("%s: [dependencies.%s] is %T, want a table", path, BuildDepsKey, raw)
			}
			for bName, bRaw := range sub {
				constraint, ok := bRaw.(string)
				if !ok {
					return deps, fmt.Errorf("%s: dependencies.%s.%s is %T, want a version constraint string",
						path, BuildDepsKey, bName, bRaw)
				}
				deps.Build[bName] = constraint
			}
			continue
		}

		constraint, ok := raw.(string)
		if !ok {
			return deps, fmt.Errorf("%s: dependencies.%s is %T, want a version constraint string",
				path, name, raw)
		}
		deps.Runtime[name] = constraint
	}

	return deps, nil
}

// WritePackage writes a TAPEPACKAGE.toml.
//
// The whole file is emitted here rather than only the dependency table, because
// viper's writer lower-cases map keys on the way OUT as well as on the way in:
// handing it a correctly-cased map and calling WriteConfig produces exactly the
// wrong file that this package exists to stop being written.
func WritePackage(path string, m PackageManifest) error {
	if m.Dependencies == nil {
		m.Dependencies = map[string]string{}
	}

	data, err := toml.Marshal(m)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	return os.WriteFile(path, data, 0o644)
}
