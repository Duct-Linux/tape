package utils

import (
	"os/exec"
	"tape/common/config"
	"tape/common/global"
	"tape/common/logger"

	"github.com/spf13/cobra"
)

func CmdPrepare(cmd *cobra.Command, moduleName string) (*logger.Logger, *exec.Cmd, error) {
	// The verbose flag's error was previously checked several statements after
	// the value had already been used.
	verbose, err := cmd.Flags().GetBool("verbose")
	if err != nil {
		return nil, nil, err
	}
	global.SetGlobals(verbose)

	log := logger.NewLogger("cli", "utils.CmdPrepare")
	outsideLog := logger.NewLogger("cli", moduleName)

	configManager, err := config.GetConfigManager()
	if err != nil {
		log.VerboseError(err.Error())
		return nil, nil, err
	}
	global.SetConfig(configManager)

	daemon, err := DaemonCheckRunning(configManager)
	if err != nil {
		log.VerboseError(err.Error())
		return nil, nil, err
	}

	return outsideLog, daemon, nil
}
