package effectrpc

import (
	"net"

	"golang.org/x/sys/unix"
)

func peerPID(conn net.Conn) int {
	pid := 0
	control(conn, func(fd int) {
		if got, err := unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID); err == nil {
			pid = got
		}
	})
	return pid
}
