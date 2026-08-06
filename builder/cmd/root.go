/*
Copyright © 2023 Yanick Stephan <me@yanick.gay>
*/
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "tape-builder",
	Short: "A tool for tape to build packages",
	Long: `A tool for tape to build packages. It will build a package using
a TAPEBUILD.toml file.`,

	// A failing build stage is not a usage error, and dumping the full help
	// text after one buries the actual message.
	SilenceUsage: true,
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "verbose output")
}
