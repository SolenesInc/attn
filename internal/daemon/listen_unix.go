package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
)

func ensurePrivateSocketDir(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if owner, ok := info.Sys().(*syscall.Stat_t); !info.IsDir() || !ok || int(owner.Uid) != os.Getuid() {
		return fmt.Errorf("the socket directory %s is not a directory of this user", dir)
	}
	return os.Chmod(dir, 0o700)
}

func listenUnixAtomically(path string) (net.Listener, error) {
	staging := path + ".listen"
	os.Remove(staging)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: staging, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	os.Remove(path)
	if err := os.Rename(staging, path); err != nil {
		listener.Close()
		os.Remove(staging)
		return nil, err
	}
	return listener, nil
}
