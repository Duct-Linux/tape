/*
Copyright © 2023 Yanick Stephan <me@yanick.gay>
*/
package cmd

import (
	"fmt"
	"os"
	"tape/builder/buildsteps"
	"tape/builder/utils"
	"tape/common/arch"
	commonUtils "tape/common/utils"

	"github.com/spf13/cobra"
)

// buildCmd represents the build command
//
// RunE rather than Run: a build that fails has to exit non-zero, or a build
// pipeline cannot tell a broken package from a good one. Returning the error
// instead of calling os.Exit also keeps the deferred cleanup below alive --
// os.Exit skips defers, which would leave the work tree behind and let a failed
// build contaminate the next one.
var buildCmd = &cobra.Command{
	Use:   "build [path]",
	Short: "Build a package",
	Long:  `Build a package using a TAPEBUILD.toml file.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		log, err := utils.CmdPrepare(cmd, "build")
		if err != nil {
			return err
		}

		// get current working directory
		pwd, err := os.Getwd()
		if err != nil {
			return err
		}
		log.VerboseInfo(fmt.Sprintf("Current working directory: %s", pwd))

		// get package directory
		pkgPath := commonUtils.ResolvePath(pwd, args[0])
		log.VerboseInfo(fmt.Sprintf("Package directory: %s", pkgPath))

		// load package config
		buildConfig, err := utils.PkgBuildConfigLoad(pkgPath)
		if err != nil {
			return err
		}

		// get target
		target, err := cmd.Flags().GetString("target")
		if err != nil {
			return err
		}
		if target == "" {
			// Default to the machine doing the build.
			target = arch.Current() + "-linux-gnu"
			log.Info("No target specified, defaulting to " + target)
		}
		if !arch.IsKnown(target) {
			// Not fatal -- a new architecture should not need a code change --
			// but a typo here silently produces packages nothing will install.
			log.Warning("target architecture " + arch.Normalize(target) + " is not one tape recognises")
		}

		// prepare it for building
		log.Info("- Preparing -")
		utils.PkgPrepare(pkgPath)
		out, err := cmd.Flags().GetString("output")
		if err != nil {
			return err
		}
		if out == "" {
			out = pwd
		} else {
			out = commonUtils.ResolvePath(pwd, out)
		}
		buildsteps.SetSettings(pwd, target, pkgPath, buildConfig, out)

		noClean, err := cmd.Flags().GetBool("no-clean")
		if err != nil {
			return err
		}
		if !noClean {
			defer utils.Cleanup(pkgPath)
		}

		// 1. download
		log.Info("- Running: Download -")
		if err := buildsteps.Stage1Download(); err != nil {
			return fmt.Errorf("download: %w", err)
		}

		// 2. prepare
		log.Info("- Running: Prepare -")
		if err := buildsteps.Stage2Prepare(); err != nil {
			return fmt.Errorf("prepare: %w", err)
		}

		// 3. build
		log.Info("- Running: Build -")
		if err := buildsteps.Stage3Build(); err != nil {
			return fmt.Errorf("build: %w", err)
		}

		// 4. install
		log.Info("- Running: Install -")
		if err := buildsteps.Stage4Install(); err != nil {
			return fmt.Errorf("install: %w", err)
		}

		// 9. wrap
		log.Info("- Running: Wrap -")
		if err := buildsteps.Stage9Wrap(); err != nil {
			return fmt.Errorf("wrap: %w", err)
		}

		log.Info("Done!")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(buildCmd)

	buildCmd.Flags().Bool("no-clean", false, "Don't clean up after building")
	buildCmd.Flags().StringP("target", "t", "", "Target to build for")
	buildCmd.Flags().StringP("output", "o", "", "Output directory for the package")
}
