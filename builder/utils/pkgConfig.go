package utils

import (
	"path"

	"github.com/spf13/viper"
)

// PkgBuildConfigName is the recipe file viper is pointed at, without its
// extension.
const PkgBuildConfigName = "TAPEBUILD"

// PkgBuildConfigPath is the same file as an actual path, for the parts of the
// recipe that must be read without viper's key lower-casing (see
// tape/common/manifest). Derived from the same constant so the two readers
// cannot end up looking at different files.
func PkgBuildConfigPath(pkgPath string) string {
	return path.Join(pkgPath, PkgBuildConfigName+".toml")
}

func PkgBuildConfigLoad(pkgPath string) (*viper.Viper, error) {
	config := viper.New()
	config.SetConfigName(PkgBuildConfigName)
	config.SetConfigType("toml")
	config.AddConfigPath(pkgPath)
	err := config.ReadInConfig()
	if err != nil {
		return nil, err
	}
	return config, nil
}
