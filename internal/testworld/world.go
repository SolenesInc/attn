package testworld

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

type World struct {
	T          testing.TB
	Dir        string
	Socket     string
	Vars       []string
	kit        *fakeagent.Kit
	Dial       func(ctx context.Context) (net.Conn, error)
	DialUnix   func() (net.Conn, error)
	WSAddr     string
	peers      []*Peer
	workspaces map[string]string
}

func Prepare(t testing.TB, wrapper string, harnesses ...fakeagent.Harness) *World {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "attn-w-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	production, err := config.CanonicalRuntimePath(config.DataDirForInstance(""))
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := config.CanonicalRuntimePath(dir); err != nil || strings.HasPrefix(resolved+"/", production+"/") {
		t.Fatalf("world directory %s resolves under production %s (%v)", dir, production, err)
	}
	w := &World{T: t, Dir: dir, Socket: filepath.Join(dir, "attn.sock")}
	w.kit = fakeagent.Install(t, dir, harnesses, wrapper)
	w.Vars = append([]string{
		"ATTN_DATA_DIR=" + dir,
		"ATTN_HARNESS_DATA_DIR=" + dir,
		"ATTN_HARNESS_NOTEBOOK_ROOT=" + filepath.Join(dir, "notebook"),
		"ATTN_CLIENT_TOKEN=",
		"ATTN_HEADLESS_TASKS=off",
		"SHELL=/bin/sh",
	}, w.kit.Env()...)
	return w
}

func (w *World) Path(elem ...string) string {
	return filepath.Join(append([]string{w.Dir, "work"}, elem...)...)
}

func (w *World) Client() *client.Client {
	return client.NewWithDial(w.Socket, w.DialUnix)
}

func (w *World) App() *Peer {
	w.T.Helper()
	token, err := os.ReadFile(filepath.Join(w.Dir, config.ClientTokenFile))
	if err != nil {
		w.T.Fatalf("read the client token the daemon minted: %v", err)
	}
	p := w.Connect(protocol.ClientHelloMessage{
		Cmd:          protocol.CmdClientHello,
		ClientKind:   "tauri-app",
		Version:      "protocol-" + protocol.ProtocolVersion,
		Capabilities: []string{protocol.CapabilityWorkspaceSessions, protocol.CapabilityBinaryPtyOutput},
		ClientToken:  protocol.Ptr(strings.TrimSpace(string(token))),
	}, nil)
	p.Initial = Await[protocol.InitialStateMessage](p, protocol.EventInitialState, nil)
	if got := protocol.Deref(p.Initial.ProtocolVersion); got != protocol.ProtocolVersion {
		w.T.Fatalf("daemon speaks protocol %q, want %q", got, protocol.ProtocolVersion)
	}
	return p
}

func (w *World) Connect(hello protocol.ClientHelloMessage, header http.Header) *Peer {
	w.T.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return w.Dial(ctx) },
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+w.WSAddr+"/ws", &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: transport},
		HTTPHeader: header,
	})
	if err != nil {
		w.T.Fatalf("dial the daemon's WebSocket: %v", err)
	}
	conn.SetReadLimit(-1)
	p := newPeer(w.T, conn)
	w.peers = append(w.peers, p)
	p.Send(hello)
	return p
}

func (w *World) ClosePeers() {
	for _, p := range w.peers {
		p.Close()
	}
	w.peers = nil
}

func (w *World) Spawn(p *Peer, h fakeagent.Harness, cwd string, opts ...func(*protocol.SpawnSessionMessage)) string {
	w.T.Helper()
	result, _, _ := w.RequestSpawn(p, h, cwd, opts...)
	if !result.Success {
		w.T.Fatalf("spawn %s in %s failed: %s", h, cwd, protocol.Deref(result.Error))
	}
	return result.ID
}

func (w *World) RequestSpawn(p *Peer, h fakeagent.Harness, cwd string, opts ...func(*protocol.SpawnSessionMessage)) (result protocol.SpawnResultMessage, workspaceID, paneID string) {
	w.T.Helper()
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		w.T.Fatal(err)
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
	paneID = "pane-" + msg.ID
	Request(p, protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: msg.WorkspaceID,
		SessionID:   msg.ID,
		PaneID:      protocol.Ptr(paneID),
	}, protocol.EventWorkspaceLayoutActionResult, func(protocol.WorkspaceLayoutActionResultMessage) bool { return true })
	result = Request(p, msg, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == msg.ID })
	return result, msg.WorkspaceID, paneID
}

func (w *World) workspace(p *Peer, dir string) string {
	w.T.Helper()
	if id, ok := w.workspaces[dir]; ok {
		return id
	}
	id := "workspace-" + filepath.Base(dir)
	Request(p, protocol.RegisterWorkspaceMessage{
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

func (w *World) Launched(sessionID string) *fakeagent.Run {
	w.T.Helper()
	return w.kit.Launched(sessionID)
}

func (w *World) HoldNextBoot() (boot func()) {
	return w.kit.HoldNextBoot()
}

func (w *World) LogDaemonTail() {
	log, err := os.ReadFile(filepath.Join(w.Dir, "daemon.log"))
	if err != nil {
		return
	}
	lines := bytes.Split(bytes.TrimRight(log, "\n"), []byte("\n"))
	lines = lines[max(0, len(lines)-80):]
	w.T.Logf("daemon.log tail:\n%s", bytes.Join(lines, []byte("\n")))
}
