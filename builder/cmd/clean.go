/*
Copyright © 2023 Yanick Stephan <me@yanick.gay>
*/
package cmd

import (
	"fmt"
	"os"
	"path"
	"tape/builder/utils"
	commonUtils "tape/common/utils"

	"github.com/spf13/cobra"
)

// cleanCmd represents the clean command
var cleanCmd = &cobra.Command{
	Use:   "clean [path]",
	Short: "Clean up the build directory",
	Long:  `Clean up the build directory`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		log, err := utils.CmdPrepare(cmd, "build")
		if err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}

		// get current working directory
		pwd, err := os.Getwd()
		if err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}
		log.VerboseInfo(fmt.Sprintf("Current working directory: %s", pwd))

		// get package directory
		pkdDir := commonUtils.ResolvePath(pwd, args[0])

		// check if TAPEBUILD file exists
		buildFilePath := path.Join(pkdDir, "TAPEBUILD.toml")
		if _, err := os.Stat(buildFilePath); os.IsNotExist(err) {
			log.Error("TAPEBUILD.toml file not found")
			os.Exit(1)
		}

		log.Info("Cleaning up build directory")
		utils.Cleanup(pkdDir)

		log.Info("Done!")
	},
}

func init() {
	rootCmd.AddCommand(cleanCmd)
}
