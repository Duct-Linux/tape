/*
Copyright © 2023 Yanick Stephan <me@yanick.gay>
*/
package cmd

import (
	"fmt"
	"os"
	"path"
	"tape/common/database"
	commonUtils "tape/common/utils"
	"tape/repo/utils"

	"github.com/spf13/cobra"
)

// createRepoCmd represents the createRepo command
var createRepoCmd = &cobra.Command{
	Use:   "create-repo [path]",
	Short: "Create a repository",
	Long:  `Create a repository`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		log, err := utils.CmdPrepare(cmd, "createRepo")
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

		repoPath := commonUtils.ResolvePath(pwd, args[0])
		log.Info(fmt.Sprintf("Creating repository at %s", repoPath))

		err = os.MkdirAll(path.Join(repoPath, "packages"), 0755)
		if err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}

		log.Info("Creating database")
		repoDbPath := path.Join(repoPath, "repo.db")
		_, err = database.RepoOpenByPath(repoDbPath)
		if err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}

		log.Info("Successfully created repository")
	},
}

func init() {
	rootCmd.AddCommand(createRepoCmd)
}
