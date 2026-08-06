/*
Copyright © 2023 Yanick Stephan <me@yanick.gay>
*/
package cmd

import (
	"fmt"
	"net"
	"tape/cli/utils"
	"tape/cli/wrapper"

	"github.com/fatih/color"
	"github.com/rodaine/table"
	"github.com/spf13/cobra"
)

// listCmd represents the list command
var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "Lists installed packages",
	Long:    `Lists the packages currently installed on this system.`,
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		log, daemon, err := utils.CmdPrepare(cmd, "list")
		if err != nil {
			utils.Fail(log, err)
			utils.Cleanup(nil, daemon)
			return
		}

		var conn net.Conn
		defer func() { utils.Cleanup(conn, daemon) }()

		explicitOnly, err := cmd.Flags().GetBool("explicit")
		if err != nil {
			utils.Fail(log, err)
			return
		}
		orphansOnly, err := cmd.Flags().GetBool("orphans")
		if err != nil {
			utils.Fail(log, err)
			return
		}

		conn, enc, dec, err := utils.UnixDial()
		if err != nil {
			utils.Fail(log, err)
			return
		}

		pkgs, err := wrapper.ListPkgs(enc, dec, explicitOnly, orphansOnly)
		if err != nil {
			utils.Fail(log, err)
			return
		}

		if len(pkgs) == 0 {
			fmt.Println("No packages installed.")
			return
		}

		headerFmt := color.New(color.FgGreen, color.Underline).SprintfFunc()
		columnFmt := color.New(color.FgYellow).SprintfFunc()

		tbl := table.New("Name", "Version", "Arch", "Repository", "Reason", "Installed")
		tbl.WithHeaderFormatter(headerFmt).WithFirstColumnFormatter(columnFmt)

		for _, pkg := range pkgs {
			tbl.AddRow(
				pkg["name"],
				pkg["version"]+"-"+pkg["subversion"],
				pkg["arch"],
				pkg["repo"],
				pkg["reason"],
				pkg["installedAt"],
			)
		}
		tbl.Print()

		fmt.Printf("\n%d package(s)\n", len(pkgs))
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().BoolP("explicit", "e", false, "Only packages installed explicitly")
	listCmd.Flags().BoolP("orphans", "o", false, "Only dependencies nothing requires any more")
}
