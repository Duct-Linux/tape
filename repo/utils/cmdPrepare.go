package utils

import (
	"tape/common/config"
	"tape/common/global"
	"tape/common/logger"

	"github.com/spf13/cobra"
)

func CmdPrepare(cmd *cobra.Command, moduleName string) (*logger.Logger, error) {
	// The verbose flag's error was previously checked several statements after
	// the value had already been used.
	verbose, err := cmd.Flags().GetBool("verbose")
	if err != nil {
		return nil, err
	}
	global.SetGlobals(verbose)

	log := logger.NewLogger("repo", "utils.CmdPrepare")
	outsideLog := logger.NewLogger("repo", moduleName)

	configManager, err := config.GetConfigManager()
	if err != nil {
		log.VerboseError(err.Error())
		return nil, err
	}
	global.SetConfig(configManager)

	return outsideLog, nil
}
