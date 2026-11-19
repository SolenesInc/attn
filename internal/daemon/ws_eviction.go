package daemon

import (
	"context"
	"net"
	"time"

	"nhooyr.io/websocket"
)

const evictionCloseGrace = 1 * time.Second

func (h *wsHub) evict(client *wsClient, reason string) {
	if !client.closeSendChannelWithStatus(websocket.StatusPolicyViolation, reason) {
		return
	}
	goTransport(func() { client.hangUp(websocket.StatusPolicyViolation, reason, evictionCloseGrace) })
}

func (c *wsClient) hangUp(code websocket.StatusCode, reason string, grace time.Duration) {
	closed := make(chan struct{})
	goTransport(func() {
		defer close(closed)
		_ = c.conn.Close(code, reason)
	})
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-closed:
	case <-timer.C:
	}
	c.abortTransport()
}

func (c *wsClient) abortTransport() {
	if c.rawConn == nil {
		_ = c.conn.CloseNow()
		return
	}
	if tcp, ok := c.rawConn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = c.rawConn.Close()
}

type rawConnKey struct{}

func withRawConn(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, rawConnKey{}, conn)
}

func rawConnFrom(ctx context.Context) net.Conn {
	conn, _ := ctx.Value(rawConnKey{}).(net.Conn)
	return conn
}
