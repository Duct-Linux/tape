package global

import (
	"encoding/gob"

	"github.com/spf13/viper"
)

var (
	verbose bool
	config  *viper.Viper
)

func SetGlobals(verboseFlag bool) {
	verbose = verboseFlag

	// needed for gob encoding
	gob.Register(map[string]string{})
	gob.Register([]map[string]string{})
	gob.Register([]string{})
}

func IsVerbose() bool {
	return verbose
}

func OverrideVerbose(verboseFlag bool) {
	verbose = verboseFlag
}

func SetConfig(configManager *viper.Viper) {
	config = configManager
}

func GetConfig() *viper.Viper {
	return config
}
