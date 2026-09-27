package testworld

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	T        testing.TB
	Dir      string
	Socket   string
	Vars     []string
	kit      *fakeagent.Kit
	Dial     func(ctx context.Context) (net.Conn, error)
	DialUnix func() (net.Conn, error)
	WSAddr   string
	peers    []*Peer
}

func Prepare(t testing.TB, wrapper string, harnesses ...fakeagent.Harness) *World {
	t.Helper()
	created, err := os.MkdirTemp("/tmp", "attn-w-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(created) })
	dir, err := filepath.EvalSymlinks(created)
	if err != nil {
		t.Fatal(err)
	}
	production, err := config.CanonicalRuntimePath(config.DataDirForInstance(""))
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := config.CanonicalRuntimePath(dir); err != nil || strings.HasPrefix(resolved+"/", production+"/") {
		t.Fatalf("world directory %s resolves under production %s (%v)", dir, production, err)
	}
	w := &World{T: t, Dir: dir, Socket: filepath.Join(dir, "attn.sock")}
	w.kit = fakeagent.Install(t, dir, harnesses, wrapper)
	// macOS asks dscl for the login shell before $SHELL; this dscl answers $SHELL
	// so a world runs the shell its test chose, never the developer's own.
	if err := os.WriteFile(filepath.Join(dir, "bin", "dscl"), []byte("#!/bin/sh\nprintf 'UserShell: %s\\n' \"$SHELL\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
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
	p := w.ConnectApp()
	p.Initial = Await[protocol.InitialStateMessage](p, protocol.EventInitialState, nil)
	if got := protocol.Deref(p.Initial.ProtocolVersion); got != protocol.ProtocolVersion {
		w.T.Fatalf("daemon speaks protocol %q, want %q", got, protocol.ProtocolVersion)
	}
	return p
}

func (w *World) ConnectApp() *Peer {
	w.T.Helper()
	token, err := os.ReadFile(filepath.Join(w.Dir, config.ClientTokenFile))
	if err != nil {
		w.T.Fatalf("read the client token the daemon minted: %v", err)
	}
	return w.Connect(protocol.ClientHelloMessage{
		Cmd:          protocol.CmdClientHello,
		ClientKind:   "tauri-app",
		Version:      "protocol-" + protocol.ProtocolVersion,
		Capabilities: []string{protocol.CapabilityBinaryPtyOutput, protocol.CapabilityKittyImages},
		ClientToken:  protocol.Ptr(strings.TrimSpace(string(token))),
	}, nil)
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

func (w *World) RequestSpawn(p *Peer, h fakeagent.Harness, cwd string, opts ...func(*protocol.SpawnSessionMessage)) (result protocol.SpawnResultMessage, desktopID, paneID string) {
	w.T.Helper()
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		w.T.Fatal(err)
	}
	msg := protocol.SpawnSessionMessage{
		Cmd:       protocol.CmdSpawnSession,
		ID:        uuid.NewString(),
		Agent:     string(h),
		Cwd:       cwd,
		ProfileID: p.SelectedProfile(),
		Placement: &protocol.SessionPlacement{},
		Cols:      100,
		Rows:      30,
	}
	for _, opt := range opts {
		opt(&msg)
	}
	if msg.Placement != nil && protocol.Deref(msg.Placement.DesktopID) == "" && p.Placed(msg.ID) {
		msg.Placement = nil
	}
	result = Request(p, msg, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == msg.ID })
	return result, protocol.Deref(result.DesktopID), protocol.Deref(result.PaneID)
}

func (w *World) Launched(sessionID string) *fakeagent.Run {
	w.T.Helper()
	return w.kit.Launched(sessionID)
}

func (w *World) HeadlessTask() *fakeagent.HeadlessTask {
	w.T.Helper()
	return w.kit.HeadlessTask()
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

func (w *World) InjectSession(id, label, dir string, agent protocol.SessionAgent) error {
	return w.inject(protocol.Session{ID: id, Label: label, Directory: dir, Agent: agent, State: protocol.SessionStateLaunching})
}

func (w *World) InjectCrewSession(id, label, dir, member string) error {
	return w.inject(protocol.Session{ID: id, Label: label, Directory: dir, Agent: protocol.SessionAgentClaude, State: protocol.SessionStateLaunching, CrewMember: protocol.Ptr(member)})
}

func (w *World) inject(session protocol.Session) error {
	conn, err := w.DialUnix()
	if err != nil {
		return err
	}
	defer conn.Close()
	msg := protocol.InjectTestSessionMessage{Cmd: protocol.CmdInjectTestSession, Session: session}
	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		return err
	}
	var resp protocol.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return err
	}
	if !resp.Ok {
		return errors.New(protocol.Deref(resp.Error))
	}
	return nil
}
