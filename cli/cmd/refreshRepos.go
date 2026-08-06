/*
Copyright © 2023 Yanick Stephan <me@yanick.gay>
*/
package cmd

import (
	"net"
	"tape/cli/utils"
	"tape/cli/wrapper"

	"github.com/spf13/cobra"
)

// refreshReposCmd represents the refreshRepos command
var refreshReposCmd = &cobra.Command{
	Use:   "refresh-repos",
	Short: "Refreshs the repositories",
	Long: `Refreshs the repositories. It downloads the repo.db files from the
repositories and updates the database.`,
	Run: func(cmd *cobra.Command, args []string) {
		log, daemon, err := utils.CmdPrepare(cmd, "refresh-repos")
		if err != nil {
			utils.Fail(log, err)
			utils.Cleanup(nil, daemon)
			return
		}

		// Deferred in a closure: `defer utils.Cleanup(conn, ...)` would
		// evaluate conn immediately, capturing nil before the dial.
		var conn net.Conn
		defer func() { utils.Cleanup(conn, daemon) }()

		conn, enc, dec, err := utils.UnixDial()
		if err != nil {
			utils.Fail(log, err)
			return
		}

		noForce, err := cmd.Flags().GetBool("no-force")
		if err != nil {
			utils.Fail(log, err)
			return
		}

		err = wrapper.RefreshRepos(enc, dec, !noForce)
		if err != nil {
			utils.Fail(log, err)
			return
		}
	},
}

func init() {
	rootCmd.AddCommand(refreshReposCmd)
	refreshReposCmd.Flags().Bool("no-force", false, "Don't force the refresh of the repositories")
}
