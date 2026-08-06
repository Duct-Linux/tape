package utils

import "github.com/spf13/viper"

func PkgBuildConfigLoad(pkgPath string) (*viper.Viper, error) {
	config := viper.New()
	config.SetConfigName("TAPEBUILD")
	config.SetConfigType("toml")
	config.AddConfigPath(pkgPath)
	err := config.ReadInConfig()
	if err != nil {
		return nil, err
	}
	return config, nil
}
