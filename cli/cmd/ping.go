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

// pingCmd represents the ping command
var pingCmd = &cobra.Command{
	Use:   "ping",
	Short: "Pings the daemon",
	Long:  `Pings the daemon to check if it's running.`,
	Run: func(cmd *cobra.Command, args []string) {
		log, daemon, err := utils.CmdPrepare(cmd, "ping")
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

		err = wrapper.Ping(enc, dec)
		if err != nil {
			utils.Fail(log, err)
			return
		}
	},
}

func init() {
	rootCmd.AddCommand(pingCmd)
}
