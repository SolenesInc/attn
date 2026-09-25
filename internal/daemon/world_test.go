package daemon_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

type world struct {
	t          *testing.T
	dir        string
	socket     string
	bubbled    bool
	harnesses  []fakeagent.Harness
	unix       net.Listener
	ws         net.Listener
	dial       func(ctx context.Context) (net.Conn, error)
	dialUnix   func() (net.Conn, error)
	daemon     *daemon.WireDaemon
	kit        *fakeagent.Kit
	peers      []*peer
	workspaces map[string]string
}

type worldOption func(*world)

func withAgents(h ...fakeagent.Harness) worldOption {
	return func(w *world) { w.harnesses = append(w.harnesses, h...) }
}

func newWorld(t *testing.T, opts ...worldOption) *world {
	t.Helper()
	w := prepareWorld(t, opts...)
	w.start()
	return w
}

func inBubble(t *testing.T, script func(t *testing.T, w *world)) {
	t.Helper()
	prepared := prepareWorld(t)
	synctest.Test(t, func(t *testing.T) {
		w := &world{t: t, dir: prepared.dir, socket: prepared.socket, bubbled: true, kit: prepared.kit}
		w.start()
		script(t, w)
	})
}

func prepareWorld(t *testing.T, opts ...worldOption) *world {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "attn-w-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	w := &world{t: t, dir: dir, socket: filepath.Join(dir, "attn.sock")}
	for _, opt := range opts {
		opt(w)
	}
	bin := filepath.Join(dir, "bin")
	toolHome := filepath.Join(dir, "toolhome")
	t.Setenv("ATTN_DATA_DIR", dir)
	t.Setenv("ATTN_CLIENT_TOKEN", "")
	t.Setenv("ATTN_TOOL_HOME", toolHome)
	t.Setenv("CODEX_HOME", filepath.Join(toolHome, ".codex"))
	t.Setenv("ATTN_HEADLESS_TASKS", "off")
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ATTN_CLAUDE_EXECUTABLE", filepath.Join(bin, string(fakeagent.Claude)))
	t.Setenv("ATTN_CODEX_EXECUTABLE", filepath.Join(bin, string(fakeagent.Codex)))
	t.Setenv("ATTN_COPILOT_EXECUTABLE", filepath.Join(bin, string(fakeagent.Copilot)))
	t.Setenv("ATTN_WRAPPER_PATH", filepath.Join(bin, "attn"))
	wrapper := ""
	if len(w.harnesses) > 0 {
		wrapper = daemon.AttnWrapper(t)
	}
	w.kit = fakeagent.Install(t, dir, w.harnesses, wrapper)
	return w
}

func (w *world) start() {
	w.t.Helper()
	w.listen()
	started, err := daemon.StartWireDaemon(w.socket, w.unix, w.ws)
	if err != nil {
		w.t.Fatalf("start daemon: %v", err)
	}
	w.daemon = started
	w.t.Cleanup(w.stop)
}

func (w *world) listen() {
	w.t.Helper()
	if w.bubbled {
		unix, ws := newPipeListener(), newPipeListener()
		w.unix, w.ws, w.dial = unix, ws, ws.dial
		w.dialUnix = func() (net.Conn, error) { return unix.dial(context.Background()) }
		return
	}
	unix, err := net.Listen("unix", w.socket)
	if err != nil {
		w.t.Fatal(err)
	}
	ws, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Setenv("ATTN_WS_PORT", strconv.Itoa(ws.Addr().(*net.TCPAddr).Port))
	w.unix, w.ws = unix, ws
	w.dial = func(ctx context.Context) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "tcp", ws.Addr().String())
	}
	w.dialUnix = func() (net.Conn, error) { return net.Dial("unix", w.socket) }
}

func (w *world) stop() {
	if w.daemon == nil {
		return
	}
	for _, p := range w.peers {
		p.close()
	}
	w.peers = nil
	if err := w.daemon.Stop(); err != nil {
		w.t.Errorf("stop daemon: %v", err)
	}
	w.daemon = nil
	if w.t.Failed() {
		w.logDaemonTail()
	}
}

func (w *world) logDaemonTail() {
	log, err := os.ReadFile(filepath.Join(w.dir, "daemon.log"))
	if err != nil {
		return
	}
	lines := bytes.Split(bytes.TrimRight(log, "\n"), []byte("\n"))
	lines = lines[max(0, len(lines)-80):]
	w.t.Logf("daemon.log tail:\n%s", bytes.Join(lines, []byte("\n")))
}

func (w *world) restart() {
	w.t.Helper()
	w.stop()
	w.start()
}

func (w *world) advance(d time.Duration) {
	w.t.Helper()
	if !w.bubbled {
		w.t.Fatal("advance moves the fake clock, which only a world inBubble has")
	}
	time.Sleep(d)
	synctest.Wait()
}

func (w *world) path(elem ...string) string {
	return filepath.Join(append([]string{w.dir, "work"}, elem...)...)
}

func (w *world) cli() *client.Client {
	return client.NewWithDial(w.socket, w.dialUnix)
}

func (w *world) app() *peer {
	w.t.Helper()
	p := w.connect(protocol.ClientHelloMessage{
		Cmd:          protocol.CmdClientHello,
		ClientKind:   "tauri-app",
		Version:      "protocol-" + protocol.ProtocolVersion,
		Capabilities: []string{protocol.CapabilityWorkspaceSessions, protocol.CapabilityBinaryPtyOutput},
		ClientToken:  protocol.Ptr(config.ClientToken()),
	}, nil)
	p.initial = await[protocol.InitialStateMessage](p, protocol.EventInitialState, nil)
	if got := protocol.Deref(p.initial.ProtocolVersion); got != protocol.ProtocolVersion {
		w.t.Fatalf("daemon speaks protocol %q, want %q", got, protocol.ProtocolVersion)
	}
	return p
}

func (w *world) connect(hello protocol.ClientHelloMessage, header http.Header) *peer {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return w.dial(ctx) },
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+w.ws.Addr().String()+"/ws", &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: transport},
		HTTPHeader: header,
	})
	if err != nil {
		w.t.Fatalf("dial the daemon's WebSocket: %v", err)
	}
	conn.SetReadLimit(-1)
	p := newPeer(w.t, conn)
	w.peers = append(w.peers, p)
	p.send(hello)
	return p
}

func (w *world) spawn(p *peer, h fakeagent.Harness, cwd string, opts ...func(*protocol.SpawnSessionMessage)) string {
	w.t.Helper()
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		w.t.Fatal(err)
	}
	msg := protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          uuid.NewString(),
		Agent:       string(h),
		Cwd:         cwd,
		WorkspaceID: w.workspace(p, cwd),
		Cols:        100,
		Rows:        30,
	}
	for _, opt := range opts {
		opt(&msg)
	}
	request(p, protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: msg.WorkspaceID,
		SessionID:   msg.ID,
		PaneID:      protocol.Ptr("pane-" + msg.ID),
	}, protocol.EventWorkspaceLayoutActionResult, func(protocol.WorkspaceLayoutActionResultMessage) bool { return true })
	result := request(p, msg, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == msg.ID })
	if !result.Success {
		w.t.Fatalf("spawn %s in %s failed: %s", h, cwd, protocol.Deref(result.Error))
	}
	return msg.ID
}

func (w *world) workspace(p *peer, dir string) string {
	w.t.Helper()
	if id, ok := w.workspaces[dir]; ok {
		return id
	}
	id := "workspace-" + filepath.Base(dir)
	request(p, protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        id,
		Title:     filepath.Base(dir),
		Directory: dir,
	}, protocol.EventWorkspaceRegistered, func(protocol.WebSocketEvent) bool { return true })
	if w.workspaces == nil {
		w.workspaces = map[string]string{}
	}
	w.workspaces[dir] = id
	return id
}

func (w *world) launched(sessionID string) *fakeagent.Run {
	w.t.Helper()
	return w.kit.Launched(sessionID)
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
