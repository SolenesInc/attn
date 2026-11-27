package fakeagent

import (
	"os"
	"syscall"
)

// WriteExecutable is os.WriteFile for a program a test runs while its
// process may fork: holding ForkLock keeps every forked child from
// inheriting the write descriptor, which would fail the next exec of the
// file with ETXTBSY.
func WriteExecutable(name string, data []byte, perm os.FileMode) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(name, data, perm)
}
