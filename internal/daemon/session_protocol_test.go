package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

func TestSessionProtocolLifecycleMatchesAppOrder(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newProtocolTestClient()
	sessionID := "session-shell-1"
	cwd := t.TempDir()
	profile, err := d.store.MostRecentlyUsedProfile()
	if err != nil {
		t.Fatal(err)
	}

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:       protocol.CmdSpawnSession,
		ID:        sessionID,
		Label:     protocol.Ptr("shell"),
		Cwd:       cwd,
		Agent:     protocol.AgentShellValue,
		ProfileID: profile.ID,
		Placement: &protocol.SessionPlacement{},
		Cols:      80,
		Rows:      24,
	})
	expectSpawnResult(t, client, sessionID, true)
	placement, placed, err := d.store.SessionPlacement(sessionID)
	if err != nil || !placed || placement.DesktopID != profile.CurrentDesktopID {
		t.Fatalf("placement = %+v placed=%v err=%v, want the profile's current desktop", placement, placed, err)
	}
	desktop, err := d.store.GetDesktop(placement.DesktopID)
	if err != nil || desktop.ActivePaneID != placement.PaneID || desktop.Panes[0].Status != profiles.PaneStatusReady || desktop.Panes[0].Title != "shell" {
		t.Fatalf("desktop = %+v err=%v, want the new agent's ready pane focused", desktop, err)
	}

	d.handleUnregisterWS(client, &protocol.UnregisterMessage{ID: sessionID})
	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("session %s still registered after its close", sessionID)
	}
	if _, placed, _ := d.store.SessionPlacement(sessionID); placed {
		t.Fatalf("session %s kept its pane after its close", sessionID)
	}
	if desktop, err := d.store.GetDesktop(placement.DesktopID); err != nil || len(desktop.Panes) != 0 {
		t.Fatalf("desktop after close = %+v err=%v, want it empty and kept", desktop, err)
	}
}

func TestSessionProtocolSpawnFailureCreatesNoPane(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &failingSpawnBackend{err: errors.New("boom")}
	client := newProtocolTestClient()
	sessionID := "session-bare-spawn-fails"

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:       protocol.CmdSpawnSession,
		ID:        sessionID,
		Label:     protocol.Ptr("bare shell"),
		Cwd:       t.TempDir(),
		Agent:     protocol.AgentShellValue,
		ProfileID: defaultProfileID(t, d.store),
		Placement: &protocol.SessionPlacement{},
		Cols:      80,
		Rows:      24,
	})
	expectSpawnResult(t, client, sessionID, false)

	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("failed bare spawn registered session %s", sessionID)
	}
	if _, placed, _ := d.store.SessionPlacement(sessionID); placed {
		t.Fatalf("failed spawn left a ghost pane for session %s", sessionID)
	}
}

func TestSessionProtocolShellSpawnsIdleNotWorking(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newProtocolTestClient()
	sessionID := "session-shell-idle"

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:       protocol.CmdSpawnSession,
		ID:        sessionID,
		Label:     protocol.Ptr("shell"),
		Cwd:       t.TempDir(),
		Agent:     protocol.AgentShellValue,
		ProfileID: defaultProfileID(t, d.store),
		Placement: &protocol.SessionPlacement{},
		Cols:      80,
		Rows:      24,
	})
	expectSpawnResult(t, client, sessionID, true)

	session := d.store.Get(sessionID)
	if session == nil {
		t.Fatalf("session %s was not registered", sessionID)
	}
	if session.State != protocol.SessionStateIdle {
		t.Fatalf("shell session state = %q, want %q", session.State, protocol.SessionStateIdle)
	}
}

func TestSessionProtocolRespawnFailureRestoresExistingSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &failingSpawnBackend{err: errors.New("boom")}
	client := newProtocolTestClient()
	originalDirectory := t.TempDir()
	original := &protocol.Session{
		ID: "existing-session", Label: "preserved", Agent: protocol.SessionAgentShell,
		Directory: originalDirectory, ProfileID: defaultProfileID(t, d.store), State: protocol.SessionStateIdle,
		StateSince: "before", StateUpdatedAt: "before", LastSeen: "before",
	}
	if err := d.store.AddChecked(original); err != nil {
		t.Fatal(err)
	}

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: original.ID, Label: protocol.Ptr("replacement"),
		Cwd: t.TempDir(), Agent: protocol.AgentShellValue, ProfileID: defaultProfileID(t, d.store), Cols: 80, Rows: 24,
	})
	expectSpawnResult(t, client, original.ID, false)
	got := d.store.Get(original.ID)
	if got == nil || got.Directory != originalDirectory || got.Label != original.Label || got.LastSeen != original.LastSeen {
		t.Fatalf("session after failed respawn = %+v, want restored %+v", got, original)
	}
}

func TestStartupPrunesRuntimesThatHaveNoSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{sessionIDs: []string{"session-kept", "runtime-orphan"}}
	d.ptyBackend = backend
	d.store.Add(&protocol.Session{ID: "session-kept", Directory: t.TempDir(), State: protocol.SessionStateIdle, Agent: protocol.SessionAgentShell})

	d.pruneRuntimesWithoutSession(context.Background())

	if removed := backend.RemovedIDs(); !slices.Equal(removed, []string{"runtime-orphan"}) {
		t.Fatalf("removed runtimes = %v, want only the one without a session", removed)
	}
}

func newProtocolTestClient() *wsClient {
	return &wsClient{
		send:            make(chan outboundMessage, 32),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
}

func expectSpawnResult(t *testing.T, client *wsClient, sessionID string, success bool) protocol.SpawnResultMessage {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var result protocol.SpawnResultMessage
			if err := json.Unmarshal(outbound.payload, &result); err != nil || result.Event != protocol.EventSpawnResult {
				continue
			}
			if result.ID != sessionID {
				continue
			}
			if result.Success != success {
				t.Fatalf("spawn success = %v, want %v; payload=%s", result.Success, success, string(outbound.payload))
			}
			return result
		case <-deadline:
			t.Fatalf("timed out waiting for spawn_result for %s", sessionID)
		}
	}
}

func expectCommandError(t *testing.T, client *wsClient, cmd, errorContains string) {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var event protocol.WebSocketEvent
			if err := json.Unmarshal(outbound.payload, &event); err != nil || event.Event != protocol.EventCommandError {
				continue
			}
			if protocol.Deref(event.Cmd) != cmd {
				continue
			}
			if !strings.Contains(protocol.Deref(event.Error), errorContains) {
				t.Fatalf("command_error error = %q, want containing %q; payload=%s", protocol.Deref(event.Error), errorContains, string(outbound.payload))
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for command_error for %s", cmd)
		}
	}
}

type failingSpawnBackend struct {
	err error
}

func (b *failingSpawnBackend) Spawn(context.Context, ptybackend.SpawnOptions) error {
	return b.err
}
func (b *failingSpawnBackend) Attach(context.Context, string, string, ...ptybackend.AttachOptions) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	return ptybackend.AttachInfo{}, nil, errors.New("attach unsupported")
}
func (b *failingSpawnBackend) Input(context.Context, string, []byte) error { return nil }
func (b *failingSpawnBackend) Resize(context.Context, string, uint16, uint16, uint16, uint16) (ptybackend.ResizeResult, error) {
	return ptybackend.ResizeResult{Changed: true}, nil
}
func (b *failingSpawnBackend) SetTheme(context.Context, string, pty.TerminalTheme) error {
	return nil
}
func (b *failingSpawnBackend) Kill(context.Context, string, syscall.Signal) error { return nil }
func (b *failingSpawnBackend) Remove(context.Context, string) error               { return nil }
func (b *failingSpawnBackend) SessionIDs(context.Context) []string                { return nil }
func (b *failingSpawnBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	return ptybackend.RecoveryReport{}, nil
}
func (b *failingSpawnBackend) Shutdown(context.Context) error { return nil }

func newDaemonForTest(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
}

type broadcastCapture struct {
	mu     sync.Mutex
	events []protocol.WebSocketEvent
}

func (c *broadcastCapture) snapshot() []protocol.WebSocketEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]protocol.WebSocketEvent, len(c.events))
	copy(out, c.events)
	return out
}

func captureBroadcasts(d *Daemon) *broadcastCapture {
	c := &broadcastCapture{}
	d.wsHub.broadcastListener = func(event *protocol.WebSocketEvent) {
		if event == nil {
			return
		}
		c.mu.Lock()
		c.events = append(c.events, *event)
		c.mu.Unlock()
	}
	return c
}
