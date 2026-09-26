package daemon_test

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/testworld"
)

type world struct {
	*testworld.World
	bubbled bool
	unix    net.Listener
	ws      net.Listener
	daemon  *daemon.WireDaemon
}

func newWorld(t *testing.T, agents ...fakeagent.Harness) *world {
	t.Helper()
	w := &world{World: prepareWorld(t, agents...)}
	w.start()
	return w
}

func inBubble(t *testing.T, script func(t *testing.T, w *world)) {
	t.Helper()
	prepared := prepareWorld(t)
	synctest.Test(t, func(t *testing.T) {
		bubbled := *prepared
		bubbled.T = t
		w := &world{World: &bubbled, bubbled: true}
		w.start()
		script(t, w)
	})
}

func prepareWorld(t *testing.T, agents ...fakeagent.Harness) *testworld.World {
	t.Helper()
	wrapper := ""
	if len(agents) > 0 {
		wrapper = testworld.AttnBinary(t)
	}
	prepared := testworld.Prepare(t, wrapper, agents...)
	for _, pair := range prepared.Vars {
		key, value, _ := strings.Cut(pair, "=")
		t.Setenv(key, value)
	}
	return prepared
}

func (w *world) start() {
	w.T.Helper()
	w.listen()
	started, err := daemon.StartWireDaemon(w.Socket, w.unix, w.ws)
	if err != nil {
		w.T.Fatalf("start daemon: %v", err)
	}
	w.daemon = started
	w.T.Cleanup(w.stop)
}

func (w *world) listen() {
	w.T.Helper()
	if w.bubbled {
		unix, ws := newPipeListener(), newPipeListener()
		w.unix, w.ws, w.Dial, w.WSAddr = unix, ws, ws.dial, ws.Addr().String()
		w.DialUnix = func() (net.Conn, error) { return unix.dial(context.Background()) }
		return
	}
	unix, err := net.Listen("unix", w.Socket)
	if err != nil {
		w.T.Fatal(err)
	}
	ws, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		w.T.Fatal(err)
	}
	w.T.Setenv("ATTN_WS_PORT", strconv.Itoa(ws.Addr().(*net.TCPAddr).Port))
	w.unix, w.ws, w.WSAddr = unix, ws, ws.Addr().String()
	w.Dial = func(ctx context.Context) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "tcp", ws.Addr().String())
	}
	w.DialUnix = func() (net.Conn, error) { return net.Dial("unix", w.Socket) }
}

func (w *world) stop() {
	if w.daemon == nil {
		return
	}
	w.ClosePeers()
	if err := w.daemon.Stop(); err != nil {
		w.T.Errorf("stop daemon: %v", err)
	}
	w.daemon = nil
	if w.T.Failed() {
		w.LogDaemonTail()
	}
}

func (w *world) restart() {
	w.T.Helper()
	w.stop()
	w.start()
}

func (w *world) advance(d time.Duration) {
	w.T.Helper()
	if !w.bubbled {
		w.T.Fatal("advance moves the fake clock, which only a world inBubble has")
	}
	time.Sleep(d)
	synctest.Wait()
}

type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr {
	return pipeAddr{}
}

func (l *pipeListener) dial(ctx context.Context) (net.Conn, error) {
	server, client := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.closed:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }
