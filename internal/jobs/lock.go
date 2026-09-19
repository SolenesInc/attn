package jobs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

var ErrAlreadyRunning = errors.New("jobs: another runner already owns this store")

const lockFileName = ".runner.lock"

func AcquireDirLock(dir string, log LogFunc) (string, error) {
	if log == nil {
		log = func(string, ...interface{}) {}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, lockFileName)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			if _, werr := f.WriteString(strconv.Itoa(os.Getpid())); werr != nil {
				_ = f.Close()
				_ = os.Remove(path)
				return "", werr
			}
			if cerr := f.Close(); cerr != nil {
				return "", cerr
			}
			return path, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
		if pid, alive := lockHolderAlive(path); alive {
			return "", fmt.Errorf("%w (held by pid %d)", ErrAlreadyRunning, pid)
		}
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return "", rmErr
		}
		log("jobs: reclaimed stale runner lock at %s", path)
	}
}

func ReleaseDirLock(path string, log LogFunc) {
	if path == "" {
		return
	}
	if log == nil {
		log = func(string, ...interface{}) {}
	}
	if pid, _ := lockHolderAlive(path); pid != 0 && pid != os.Getpid() {
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log("jobs: release runner lock %s: %v", path, err)
	}
}

func lockHolderAlive(path string) (pid int, alive bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	if pid == os.Getpid() {
		return pid, true
	}
	return pid, processAlive(pid)
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}
