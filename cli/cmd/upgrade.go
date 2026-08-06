/*
Copyright © 2023 Yanick Stephan <me@yanick.gay>
*/
package cmd

import (
	"fmt"
	"tape/cli/utils"
	"tape/cli/wrapper"
	"tape/common/enums"
	"tape/common/structs"

	"github.com/fatih/color"
	"github.com/rodaine/table"
	"github.com/spf13/cobra"
)

// upgradeCmd represents the upgrade command
var upgradeCmd = &cobra.Command{
	Use:     "upgrade [package...]",
	Aliases: []string{"up"},
	Short:   "Upgrades installed packages",
	Long: `Upgrades installed packages to the newest build available.

With no arguments every installed package is considered. Dependencies of the
upgraded packages are resolved and installed as needed.`,
	Run: func(cmd *cobra.Command, args []string) {
		log, daemon, err := utils.CmdPrepare(cmd, "upgrade")
		if err != nil {
			utils.Fail(log, err)
			utils.Cleanup(nil, daemon)
			return
		}

		if err := runUpgrade(cmd, args); err != nil {
			utils.Fail(log, err)
		}

		utils.Cleanup(nil, daemon)
	},
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	// Always refresh first: an upgrade against a stale index is meaningless.
	noRefresh, err := cmd.Flags().GetBool("no-refresh")
	if err != nil {
		return err
	}
	if !noRefresh {
		if err := refreshForUpgrade(); err != nil {
			return fmt.Errorf("refreshing repositories: %w", err)
		}
	}

	candidates, err := checkUpgrades(args)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		fmt.Println("Everything is up to date.")
		return nil
	}

	headerFmt := color.New(color.FgGreen, color.Underline).SprintfFunc()
	columnFmt := color.New(color.FgYellow).SprintfFunc()

	tbl := table.New("Name", "Installed", "Available", "Repository")
	tbl.WithHeaderFormatter(headerFmt).WithFirstColumnFormatter(columnFmt)
	for _, c := range candidates {
		tbl.AddRow(
			c["name"],
			c["currentVersion"]+"-"+c["currentSubversion"],
			c["version"]+"-"+c["subversion"],
			c["repo"],
		)
	}
	fmt.Println("The following packages will be upgraded:")
	tbl.Print()

	yes, err := cmd.Flags().GetBool("yes")
	if err != nil {
		return err
	}
	if !yes && !utils.Confirm("Proceed with the upgrade?") {
		fmt.Println("Aborting...")
		return nil
	}

	// An upgrade is an ordinary install of a newer archive: resolving each
	// candidate pulls in any dependency the new version introduced.
	names := make([]string, 0, len(candidates))
	for _, c := range candidates {
		names = append(names, c["name"])
	}

	resolved, err := resolvePackages(names)
	if err != nil {
		return err
	}

	fmt.Println("Downloading packages...")
	paths, err := downloadPackages(resolved)
	if err != nil {
		return err
	}

	// Upgraded packages keep the reason they already had; anything newly pulled
	// in is a dependency. Treating the named set as explicit here would silently
	// promote dependencies to explicit on every upgrade.
	explicit := make(map[string]struct{}, len(names))
	for _, name := range names {
		explicit[name] = struct{}{}
	}

	return installPackages(paths, resolved, explicit)
}

func refreshForUpgrade() error {
	conn, enc, dec, err := utils.UnixDial()
	if err != nil {
		return err
	}
	defer conn.Close()

	return wrapper.RefreshRepos(enc, dec, true)
}

func checkUpgrades(names []string) ([]map[string]string, error) {
	conn, enc, dec, err := utils.UnixDial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var data interface{}
	if len(names) > 0 {
		data = names
	}
	if err := enc.Encode(structs.Request{
		Type: enums.RequestTypeCheckUpgrades,
		Data: data,
	}); err != nil {
		return nil, err
	}

	var candidates []map[string]string
	err = utils.UnixDecode(dec, nil, nil, nil, func(resData interface{}) {
		if list, ok := resData.([]map[string]string); ok {
			candidates = list
		}
	})
	if err != nil {
		return nil, err
	}

	return candidates, nil
}

func init() {
	rootCmd.AddCommand(upgradeCmd)
	upgradeCmd.Flags().BoolP("yes", "y", false, "Skip confirmation")
	upgradeCmd.Flags().Bool("no-refresh", false, "Do not refresh repositories first")
}
