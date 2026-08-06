/*
Copyright © 2023 Yanick Stephan <me@yanick.gay>
*/
package cmd

import (
	"fmt"
	"net"
	"tape/cli/utils"
	"tape/cli/wrapper"

	"github.com/spf13/cobra"
)

// queryCmd represents the query command
var queryCmd = &cobra.Command{
	Use:   "query [package]",
	Short: "Query a package",
	Long:  `Query a package`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		log, daemon, err := utils.CmdPrepare(cmd, "query")
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

		resolveDependencies, err := cmd.Flags().GetBool("resolve-dependencies")
		if err != nil {
			utils.Fail(log, err)
			return
		}

		pkgs, err := wrapper.QueryPkg(enc, dec, args[0], resolveDependencies)
		if err != nil {
			utils.Fail(log, err)
			return
		}

		// A miss comes back as a result carrying an "error" key rather than a
		// Go error, so it has to be checked explicitly -- this command used to
		// print the miss as though it were a hit and exit 0.
		for _, pkg := range pkgs {
			if errMsg := pkg["error"]; errMsg != "" {
				utils.Failf(log, "%s: %s", pkg["name"], errMsg)
				fmt.Printf("%s: %s\n", pkg["name"], errMsg)
				return
			}
		}

		wrapper.PrintPkgs(pkgs)
	},
}

func init() {
	rootCmd.AddCommand(queryCmd)
	queryCmd.Flags().BoolP("resolve-dependencies", "r", false, "Resolve dependencies")
}
