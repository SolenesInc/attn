package effectrpc

import (
	"net"
	"syscall"
)

// PeerPID is the process at the other end of the socket, or 0 when the platform cannot say.
func (c *Client) PeerPID() int { return peerPID(c.conn) }

func control(conn net.Conn, f func(fd int)) {
	raw, ok := conn.(syscall.Conn)
	if !ok {
		return
	}
	if rc, err := raw.SyscallConn(); err == nil {
		_ = rc.Control(func(fd uintptr) { f(int(fd)) })
	}
}
