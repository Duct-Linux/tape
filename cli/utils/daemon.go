package utils

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"tape/common/global"
	"tape/common/logger"
	"time"

	"github.com/spf13/viper"
)

// DaemonCheckRunning reports whether the daemon is reachable, optionally
// starting one.
//
// Liveness is decided by connecting to the socket, not by the presence of a pid
// file. The pid file is written only in -d mode, so a foreground daemon read as
// "not running" and the CLI refused to talk to a perfectly healthy one; a stale
// pid file had the opposite effect, reporting a dead daemon as up.
func DaemonCheckRunning(configManager *viper.Viper) (*exec.Cmd, error) {
	log := logger.NewLogger("cli", "utils.CheckDaemonRunning")

	socket := configManager.GetString("daemon.socket")

	log.VerboseInfo("checking if daemon is running")
	if daemonReachable(socket) {
		log.VerboseInfo("daemon is reachable")
		return nil, nil
	}

	log.VerboseInfo("daemon is not reachable")
	if configManager.GetBool("cli.daemon-start") {
		return daemonStart(configManager)
	}

	return nil, fmt.Errorf("the tape daemon is not running (no socket at %s)", socket)
}

// daemonReachable probes the socket. A connect that succeeds means something is
// listening; anything else means it is not usable.
func daemonReachable(socket string) bool {
	if socket == "" {
		return false
	}
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func daemonStart(configManager *viper.Viper) (*exec.Cmd, error) {
	log := logger.NewLogger("cli", "utils.startDaemon")

	log.VerboseInfo("attempting to start daemon")

	var suffix []string
	if global.IsVerbose() {
		suffix = append(suffix, "-v")
	}

	daemon := exec.Command("/usr/bin/taped", suffix...)
	daemon.Stdout = os.Stdout
	daemon.Stderr = os.Stderr
	daemon.Stdin = os.Stdin
	err := daemon.Start()
	if err != nil {
		log.VerboseError(err.Error())
		return nil, err
	}

	// Wait for the socket, but not forever: if taped fails to start, this loop
	// used to spin until the user killed the CLI.
	const (
		startupTimeout = 15 * time.Second
		pollInterval   = 100 * time.Millisecond
	)

	socket := configManager.GetString("daemon.socket")
	deadline := time.Now().Add(startupTimeout)

	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", socket)
		if err == nil {
			conn.Close()
			return daemon, nil
		}

		// If the process already died there is nothing to wait for.
		if daemon.ProcessState != nil && daemon.ProcessState.Exited() {
			return nil, fmt.Errorf("daemon exited during startup with status %d", daemon.ProcessState.ExitCode())
		}

		time.Sleep(pollInterval)
	}

	// Do not leave an orphan behind if it never came up.
	if daemon.Process != nil {
		_ = daemon.Process.Kill()
		_ = daemon.Wait()
	}

	return nil, fmt.Errorf("daemon did not create %s within %s", socket, startupTimeout)
}
