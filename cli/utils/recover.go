package utils

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"sync/atomic"
	"tape/common/logger"
)

// exitCode is the status the CLI will exit with. It exists because cobra's Run
// functions cannot return an error, and because a failure has to survive until
// the deferred cleanup runs.
var exitCode atomic.Int32

// Fail records that the command failed. The process exits non-zero once
// cleanup has run.
//
// The CLI previously ended every command -- success or failure -- with
// os.Exit(0), so a missing daemon, a package that could not be found, and a
// failed download all reported success to the shell.
func Fail(log *logger.Logger, err error) {
	if err != nil && log != nil {
		log.Error(err.Error())
	}
	exitCode.Store(1)
}

// Failf records a failure with a formatted message.
func Failf(log *logger.Logger, format string, args ...interface{}) {
	Fail(log, fmt.Errorf(format, args...))
}

// Cleanup closes the connection, stops a daemon this process started, and exits
// with the recorded status. Defer it once per command.
//
// conn is passed by value. The previous signature took *net.Conn and guarded
// with `if conn != nil`, which is always true for the address of a local
// variable; on a failed dial that dereferenced a nil net.Conn interface and
// panicked -- inside a deferred function, after its own recover() had already
// run, so nothing could catch it.
func Cleanup(conn net.Conn, daemon *exec.Cmd) {
	log := logger.NewLogger("cli", "utils.Cleanup")

	if r := recover(); r != nil {
		log.Error(fmt.Sprintf("recovered from panic: %v", r))
		exitCode.Store(1)
	}

	if conn != nil {
		if err := conn.Close(); err != nil {
			log.VerboseError(err.Error())
		}
	}

	if err := killDaemon(daemon); err != nil {
		log.VerboseError(err.Error())
	}

	os.Exit(int(exitCode.Load()))
}

func killDaemon(daemon *exec.Cmd) error {
	if daemon == nil || daemon.Process == nil {
		return nil
	}
	if err := daemon.Process.Signal(os.Interrupt); err != nil {
		return err
	}
	return daemon.Wait()
}
