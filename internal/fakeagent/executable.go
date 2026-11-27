package fakeagent

import (
	"os"
	"path/filepath"
	"syscall"
)

// WriteExecutable writes a program a test runs while its process may fork,
// or while another process may be running the program it replaces. It writes
// a new file and renames it over name, because Linux refuses to open a
// running program for writing with ETXTBSY, and an in-place write would let a
// concurrent run see a truncated script. Holding ForkLock while the file is
// open keeps every forked child from inheriting the write descriptor, which
// would fail the next exec of the file with ETXTBSY.
func WriteExecutable(name string, data []byte, perm os.FileMode) error {
	staged, err := writeStaged(name, data, perm)
	if err != nil {
		return err
	}
	if err := os.Rename(staged, name); err != nil {
		os.Remove(staged)
		return err
	}
	return nil
}

func writeStaged(name string, data []byte, perm os.FileMode) (string, error) {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	f, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".*")
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(perm)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
