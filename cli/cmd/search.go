/*
Copyright © 2023 Yanick Stephan <me@yanick.gay>
*/
package cmd

import (
	"fmt"
	"tape/cli/utils"
	"tape/common/enums"
	"tape/common/structs"

	"github.com/fatih/color"
	"github.com/rodaine/table"
	"github.com/spf13/cobra"
)

// searchCmd represents the search command
var searchCmd = &cobra.Command{
	Use:     "search [term]",
	Aliases: []string{"s"},
	Short:   "Searches the repositories for a package",
	Long:    `Searches every enabled repository for packages whose name contains the term.`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		log, daemon, err := utils.CmdPrepare(cmd, "search")
		if err != nil {
			utils.Fail(log, err)
			utils.Cleanup(nil, daemon)
			return
		}

		results, err := searchPackages(args[0])
		if err != nil {
			utils.Fail(log, err)
			utils.Cleanup(nil, daemon)
			return
		}

		if len(results) == 0 {
			fmt.Printf("No packages matching %q.\n", args[0])
			utils.Cleanup(nil, daemon)
			return
		}

		headerFmt := color.New(color.FgGreen, color.Underline).SprintfFunc()
		columnFmt := color.New(color.FgYellow).SprintfFunc()

		tbl := table.New("Name", "Version", "Arch", "Repository", "Installed")
		tbl.WithHeaderFormatter(headerFmt).WithFirstColumnFormatter(columnFmt)
		for _, pkg := range results {
			marker := ""
			if pkg["installed"] == "yes" {
				marker = "yes"
			}
			tbl.AddRow(pkg["name"], pkg["version"]+"-"+pkg["subversion"], pkg["arch"], pkg["repo"], marker)
		}
		tbl.Print()
		fmt.Printf("\n%d result(s)\n", len(results))

		utils.Cleanup(nil, daemon)
	},
}

func searchPackages(query string) ([]map[string]string, error) {
	conn, enc, dec, err := utils.UnixDial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := enc.Encode(structs.Request{
		Type: enums.RequestTypeSearchPkgs,
		Data: query,
	}); err != nil {
		return nil, err
	}

	var results []map[string]string
	err = utils.UnixDecode(dec, nil, nil, nil, func(resData interface{}) {
		if list, ok := resData.([]map[string]string); ok {
			results = list
		}
	})
	if err != nil {
		return nil, err
	}

	return results, nil
}

func init() {
	rootCmd.AddCommand(searchCmd)
}
