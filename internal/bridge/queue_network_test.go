package bridge_test

import (
	"context"
	"net"
	"runtime"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"golang.org/x/sys/unix"
)

const queueLowWaterBytes = 8 << 10

type shapedListener struct {
	net.Listener
	wrap func(net.Conn) (net.Conn, error)
}

func (l *shapedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	wrapped, err := l.wrap(c)
	if err != nil {
		_ = c.Close()
	}
	return wrapped, err
}

func smallSocket(c net.Conn) error {
	tcp := c.(*net.TCPConn)
	socket, err := tcp.SyscallConn()
	if err != nil {
		return err
	}
	var setErr error
	if err := socket.Control(func(fd uintptr) { setErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_MAXSEG, 512) }); err != nil {
		return err
	}
	if setErr != nil {
		return setErr
	}
	if runtime.GOOS == "linux" {
		return tcp.SetReadBuffer(32 << 10)
	}
	return tcp.SetReadBuffer(4096)
}

func tcpPair(t *testing.T, w *pairWorld, wrap func(net.Conn, bool) (net.Conn, error)) (*bridge.Conn, *bridge.Conn) {
	t.Helper()
	transform := func(c net.Conn, home bool) (net.Conn, error) {
		if wrap == nil {
			return c, nil
		}
		return wrap(c, home)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	w.startOn(&shapedListener{Listener: listener, wrap: func(c net.Conn) (net.Conn, error) { return transform(c, false) }})
	client, _, err := bridge.Dial(w.ctx, bridge.DialConfig{Address: listener.Addr().String(), Self: home, AgentSocket: "agent", Handlers: w.cfg.Handlers, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" {
			return w.dialContext(ctx, network, address)
		}
		c, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		wrapped, err := transform(c, true)
		if err != nil {
			_ = c.Close()
		}
		return wrapped, err
	}})
	if err != nil {
		t.Fatal(err)
	}
	return client, <-w.server
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }
