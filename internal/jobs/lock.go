package jobs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

var ErrAlreadyRunning = errors.New("jobs: another runner already owns this store")

const lockFileName = ".runner.lock"

type DirLock struct {
	file *os.File
	path string
	log  LogFunc
	once sync.Once
}

func AcquireDirLock(dir string, log LogFunc) (*DirLock, error) {
	if log == nil {
		log = func(string, ...interface{}) {}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, lockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open runner lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		pid := lockHolderPID(f)
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			if pid > 0 {
				return nil, fmt.Errorf("acquire runner lock %s: %w (held by pid %d)", path, ErrAlreadyRunning, pid)
			}
			return nil, fmt.Errorf("acquire runner lock %s: %w (holder pid unknown)", path, ErrAlreadyRunning)
		}
		return nil, fmt.Errorf("acquire runner lock %s: %w", path, err)
	}

	lock := &DirLock{file: f, path: path, log: log}
	if err := f.Truncate(0); err != nil {
		lock.Release()
		return nil, fmt.Errorf("truncate runner lock %s: %w", path, err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		lock.Release()
		return nil, fmt.Errorf("seek runner lock %s: %w", path, err)
	}
	if _, err := f.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		lock.Release()
		return nil, fmt.Errorf("write runner lock %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		lock.Release()
		return nil, fmt.Errorf("sync runner lock %s: %w", path, err)
	}
	return lock, nil
}

func (l *DirLock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func (l *DirLock) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
			l.log("jobs: unlock runner lock %s: %v", l.path, err)
		}
		if err := l.file.Close(); err != nil {
			l.log("jobs: close runner lock %s: %v", l.path, err)
		}
	})
}

func lockHolderPID(f *os.File) int {
	if _, err := f.Seek(0, 0); err != nil {
		return 0
	}
	data := make([]byte, 64)
	n, err := f.Read(data)
	if err != nil && n == 0 {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data[:n])))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}
