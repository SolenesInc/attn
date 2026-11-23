package effectrpc

import (
	"errors"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// PeerPID is the process at the other end of the socket, or 0 when the platform cannot say.
func (c *Client) PeerPID() int { return peerPID(c.conn) }

// HungUp reports whether the server has closed its end, which holds as soon as its process has exited.
func (c *Client) HungUp() bool {
	select {
	case <-c.done:
		return true
	default:
	}
	hungUp := true
	control(c.conn, func(fd int) {
		n, _, err := unix.Recvfrom(fd, make([]byte, 1), unix.MSG_PEEK|unix.MSG_DONTWAIT)
		hungUp = n == 0 && err == nil || err != nil && !errors.Is(err, unix.EAGAIN)
	})
	return hungUp
}

func control(conn net.Conn, f func(fd int)) {
	raw, ok := conn.(syscall.Conn)
	if !ok {
		return
	}
	if rc, err := raw.SyscallConn(); err == nil {
		_ = rc.Control(func(fd uintptr) { f(int(fd)) })
	}
}
