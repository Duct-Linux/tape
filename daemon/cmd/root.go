/*
Copyright © 2023 Yanick Stephan <me@yanick.gay>
*/
package cmd

import (
	"os"
	"tape/common/config"
	"tape/common/global"
	"tape/common/logger"
	"tape/daemon/loop"

	"github.com/sevlyar/go-daemon"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "taped",
	Short: "The daemon for tape",
	Long: `The daemon for tape. It handles all the requests from the client and
manages the packages.`,
	Run: run,
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolP("daemon", "d", false, "Start as daemon (in background)")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Verbose output")
}

func run(cmd *cobra.Command, args []string) {
	verbose, err := cmd.Flags().GetBool("verbose")
	log := logger.NewLogger("daemon", "cmd.run")
	if err != nil {
		log.VerboseError(err.Error())
		return
	}

	// config manager
	configManager, err := config.GetConfigManager()
	if err != nil {
		log.VerboseError(err.Error())
		return
	}

	global.SetGlobals(verbose)
	// The daemon never published its config, so every handler that reads
	// global.GetConfig() (sysroot, installed-db, cache-dir) saw nil.
	global.SetConfig(configManager)

	// TODO: Check if the daemon is already running

	// daemon
	if daemon.WasReborn() {
		log.Info("Daemon started (in background)")
		global.OverrideVerbose(true) // so that the logger prints to the log file
		loop.StartUnixLoop(configManager)
		return
	}

	if cmd.Flag("daemon").Value.String() == "true" {
		cntxt := &daemon.Context{
			PidFileName: configManager.GetString("daemon.pid"),
			PidFilePerm: 0644,
			LogFileName: configManager.GetString("daemon.log"),
			LogFilePerm: 0640,
			WorkDir:     ".",
			Umask:       027,
		}
		d, err := cntxt.Reborn()
		if err != nil {
			log.VerboseError(err.Error())
			return
		}
		if d != nil {
			return
		}
		defer cntxt.Release()
	}

	log.VerboseInfo("Daemon started (in foreground)")
	loop.StartUnixLoop(configManager)
}
