package loop

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"tape/common/logger"
	"time"

	"github.com/spf13/viper"
)

// maxConcurrentConnections bounds in-flight handlers. Unbounded goroutine
// spawning let any local process exhaust the daemon's memory and fd budget.
const maxConcurrentConnections = 64

func StartUnixLoop(configManager *viper.Viper) {
	log := logger.NewLogger("daemon", "loop.StartUnixLoop")

	socketPath := configManager.GetString("daemon.socket")

	l, err := listen(socketPath, configManager, log)
	if err != nil {
		// Startup failures used to be invisible: VerboseError is a no-op
		// without --verbose, so a stale socket meant the daemon exited
		// silently with status 0 and no output at all.
		log.Error(fmt.Sprintf("cannot listen on %s: %s", socketPath, err.Error()))
		return
	}
	defer l.Close()

	log.Info(fmt.Sprintf("listening on %s", socketPath))

	cancelChan := make(chan os.Signal, 1)
	signal.Notify(cancelChan, syscall.SIGTERM, syscall.SIGINT)

	// closed was a plain bool written by the signal goroutine and read by the
	// accept goroutine -- a data race the race detector flags immediately.
	var closed atomic.Bool

	var wg sync.WaitGroup
	slots := make(chan struct{}, maxConcurrentConnections)

	go func() {
		var backoff time.Duration
		for {
			conn, err := l.Accept()
			if err != nil {
				if closed.Load() {
					return
				}

				// A persistent accept error (EMFILE under fd exhaustion, say)
				// used to spin this loop at 100% CPU. Back off instead.
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					continue
				}
				backoff = nextBackoff(backoff)
				log.Error(fmt.Sprintf("accept failed, retrying in %s: %s", backoff, err.Error()))
				time.Sleep(backoff)
				continue
			}
			backoff = 0

			// Apply backpressure rather than spawning without limit.
			select {
			case slots <- struct{}{}:
			default:
				log.Error("connection refused: too many concurrent clients")
				conn.Close()
				continue
			}

			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-slots }()
				handleConnection(conn)
			}()
		}
	}()

	sig := <-cancelChan
	log.Info(fmt.Sprintf("caught signal %v, shutting down", sig))

	closed.Store(true)
	l.Close()

	// Wait for in-flight work instead of tearing downloads out mid-write.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Info("all connections finished")
	case <-time.After(10 * time.Second):
		log.Warning("timed out waiting for in-flight connections")
	}
}

// listen binds the Unix socket, clearing a stale node first and tightening the
// permissions afterwards.
func listen(socketPath string, configManager *viper.Viper, log *logger.Logger) (net.Listener, error) {
	// A SIGKILLed daemon leaves its socket file behind and bind() then fails
	// with EADDRINUSE forever. Remove it -- but only when nothing is actually
	// listening, so a healthy daemon never has its socket unlinked underneath.
	if _, err := os.Stat(socketPath); err == nil {
		probe, err := net.DialTimeout("unix", socketPath, time.Second)
		if err == nil {
			probe.Close()
			return nil, fmt.Errorf("another daemon is already listening on %s", socketPath)
		}
		log.Info(fmt.Sprintf("removing stale socket %s", socketPath))
		if err := os.Remove(socketPath); err != nil {
			return nil, fmt.Errorf("removing stale socket: %w", err)
		}
	}

	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}

	// net.Listen creates the node as 0777&^umask -- world-connectable under
	// root's usual umask. The daemon does privileged work and performs no
	// authentication whatsoever, so the socket must not be reachable by
	// unprivileged processes.
	mode := os.FileMode(configManager.GetInt("daemon.socket-mode"))
	if mode == 0 {
		mode = 0660
	}
	if err := os.Chmod(socketPath, mode); err != nil {
		l.Close()
		return nil, fmt.Errorf("securing socket permissions: %w", err)
	}

	return l, nil
}

func nextBackoff(current time.Duration) time.Duration {
	const (
		minBackoff = 5 * time.Millisecond
		maxBackoff = time.Second
	)
	if current == 0 {
		return minBackoff
	}
	if next := current * 2; next < maxBackoff {
		return next
	}
	return maxBackoff
}
