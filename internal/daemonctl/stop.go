package daemonctl

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	stopSigtermWait = 5 * time.Second
	stopSigkillWait = 2 * time.Second
)

// NonDaemonHolderSentinel is written into the pid file by non-daemon lock
// holders, so a concurrent Stop never trusts a pid the current holder didn't write.
const NonDaemonHolderSentinel = "non-daemon-holder"

// Only EWOULDBLOCK means the lock is held; every other flock error fails closed.
var flockFn = syscall.Flock

type StopResult struct {
	Stopped bool
	Forced  bool
	PID     int
	Note    string
}

// The pid file's exclusive flock is the liveness+ownership gate: an acquirable lock means
// any pid on disk is stale and is never signaled. Not running is a nil error with a Note.
func Stop(pidPath string) (StopResult, error) {
	lockFile, err := os.OpenFile(pidPath, os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return StopResult{Note: "not running (no pid file)"}, nil
	}
	if err != nil {
		return StopResult{}, fmt.Errorf("could not open pid file: %w", err)
	}
	if flockErr := flockFn(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); flockErr == nil {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
		return StopResult{Note: "not running (stale pid file)"}, nil
	} else if !errors.Is(flockErr, syscall.EWOULDBLOCK) {
		lockFile.Close()
		return StopResult{}, fmt.Errorf("cannot determine daemon state: %w", flockErr)
	}
	lockFile.Close()

	// Lock held: trust only content written under it. Only numeric content is signalable.
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return StopResult{}, fmt.Errorf("could not read pid file: %w", err)
	}
	pidText := strings.TrimSpace(string(data))
	if pidText == NonDaemonHolderSentinel {
		return StopResult{Note: "not running (daemon lock held by another attn process, e.g. a database restore in progress)"}, nil
	}
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		return StopResult{}, fmt.Errorf("malformed pid file %q", pidText)
	}
	// Never signal our own process tree.
	if pid == os.Getpid() || pid == os.Getppid() {
		return StopResult{}, fmt.Errorf("refusing to stop pid %d: it is this command's own process tree", pid)
	}
	// Positive proof required: the pid must hold the file open right now.
	holds, err := pidHoldsPIDFile(pid, pidPath)
	if err != nil {
		return StopResult{}, fmt.Errorf("could not verify pid %d holds the daemon lock: %w", pid, err)
	}
	if !holds {
		return StopResult{Note: fmt.Sprintf("not running (pid %d does not hold the daemon lock — if a daemon is starting up right now, retry in a moment)", pid)}, nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			return StopResult{Note: "not running (stale pid file)"}, nil
		}
		return StopResult{}, fmt.Errorf("SIGTERM pid %d failed: %w", pid, err)
	}
	if processGoneWithin(pid, stopSigtermWait) {
		return StopResult{Stopped: true, PID: pid}, nil
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	if processGoneWithin(pid, stopSigkillWait) {
		return StopResult{Stopped: true, Forced: true, PID: pid}, nil
	}
	return StopResult{}, fmt.Errorf("pid %d did not exit after SIGKILL", pid)
}

func processGoneWithin(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}
