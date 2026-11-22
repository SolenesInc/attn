package effectrpc

import (
	"net"

	"golang.org/x/sys/unix"
)

func peerPID(conn net.Conn) int {
	pid := 0
	control(conn, func(fd int) {
		if cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil {
			pid = int(cred.Pid)
		}
	})
	return pid
}
