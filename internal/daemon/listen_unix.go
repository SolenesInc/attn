package daemon

import (
	"net"
	"os"
)

// bind() creates the socket file before listen() accepts, and a client that
// watches the directory connects in that gap and is refused. Listen elsewhere, then rename in.
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
