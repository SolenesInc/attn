package bridge

import (
	"net"
	"sync/atomic"
	"time"
)

type updateActivityConn struct {
	net.Conn
	active atomic.Bool
}

func (c *updateActivityConn) start() {
	c.active.Store(true)
	_ = c.Conn.SetDeadline(time.Now().Add(LostAfter))
}

func (c *updateActivityConn) progress(n int) {
	if n > 0 && c.active.Load() {
		_ = c.Conn.SetDeadline(time.Now().Add(LostAfter))
	}
}

func (c *updateActivityConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.progress(n)
	return n, err
}
func (c *updateActivityConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.progress(n)
	return n, err
}
