/*
Copyright © 2023 Yanick Stephan <me@yanick.gay>
*/
package cmd

import (
	"fmt"
	"net"
	"strings"
	"tape/cli/utils"
	"tape/cli/wrapper"

	"github.com/spf13/cobra"
)

// removeCmd represents the remove command
var removeCmd = &cobra.Command{
	Use:     "remove [package...]",
	Aliases: []string{"rm"},
	Short:   "Removes an installed package",
	Long: `Removes one or more installed packages.

Files are deleted using the manifest recorded at install time, so only files
the package owns are touched. Files modified since installation are left in
place. Removal is refused if another installed package still depends on it,
unless --force is given.`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		log, daemon, err := utils.CmdPrepare(cmd, "remove")
		if err != nil {
			utils.Fail(log, err)
			utils.Cleanup(nil, daemon)
			return
		}

		var conn net.Conn
		defer func() { utils.Cleanup(conn, daemon) }()

		force, err := cmd.Flags().GetBool("force")
		if err != nil {
			utils.Fail(log, err)
			return
		}

		yes, err := cmd.Flags().GetBool("yes")
		if err != nil {
			utils.Fail(log, err)
			return
		}

		if !yes {
			fmt.Println("The following packages will be removed:")
			for _, name := range args {
				fmt.Printf("  %s\n", name)
			}
			if !utils.Confirm("Do you want to remove these packages?") {
				fmt.Println("Aborting...")
				return
			}
		}

		var allOrphans []string
		for _, name := range args {
			c, enc, dec, err := utils.UnixDial()
			if err != nil {
				utils.Fail(log, err)
				return
			}

			result, err := wrapper.RemovePkg(enc, dec, name, force)
			c.Close()
			if err != nil {
				utils.Failf(log, "removing %s: %s", name, err)
				return
			}

			fmt.Printf("Removed %s (%s files)\n", result.Name, result.FilesRemoved)
			allOrphans = append(allOrphans, result.Orphans...)
		}

		if len(allOrphans) > 0 {
			fmt.Printf("\nNo longer required by anything: %s\n", strings.Join(dedupeStrings(allOrphans), ", "))
			fmt.Println("Remove them with: tape remove <name>")
		}
	},
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func init() {
	rootCmd.AddCommand(removeCmd)
	removeCmd.Flags().BoolP("force", "f", false, "Remove even if other packages depend on it")
	removeCmd.Flags().BoolP("yes", "y", false, "Skip confirmation")
}
