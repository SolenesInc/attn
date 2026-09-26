package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/workspacelayout"
)

func newWorkspaceProtocolTestClient() *wsClient {
	return &wsClient{
		send:            make(chan outboundMessage, 32),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
}

func expectWorkspaceLayoutActionResult(t *testing.T, client *wsClient, action, workspaceID, paneID string, success bool) {
	t.Helper()
	expectWorkspaceLayoutActionResultIDs(t, client, action, workspaceID, paneID, "", "", success)
}

func expectWorkspaceLayoutActionResultIDs(t *testing.T, client *wsClient, action, workspaceID, paneID, splitID, tileID string, success bool) {
	t.Helper()
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, action, workspaceID, paneID, splitID, tileID, "", success)
}

func expectWorkspaceLayoutActionResultIDsAndRequestID(t *testing.T, client *wsClient, action, workspaceID, paneID, splitID, tileID, requestID string, success bool) {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var result protocol.WorkspaceLayoutActionResultMessage
			if err := json.Unmarshal(outbound.payload, &result); err != nil || result.Event != protocol.EventWorkspaceLayoutActionResult {
				continue
			}
			if result.Action != action || result.WorkspaceID != workspaceID {
				continue
			}
			if result.Success != success {
				t.Fatalf("workspace action success = %v, want %v; payload=%s", result.Success, success, string(outbound.payload))
			}
			if got := protocol.Deref(result.PaneID); got != paneID {
				t.Fatalf("workspace action pane_id = %q, want %q; payload=%s", got, paneID, string(outbound.payload))
			}
			if got := protocol.Deref(result.SplitID); got != splitID {
				t.Fatalf("workspace action split_id = %q, want %q; payload=%s", got, splitID, string(outbound.payload))
			}
			if got := protocol.Deref(result.TileID); got != tileID {
				t.Fatalf("workspace action tile_id = %q, want %q; payload=%s", got, tileID, string(outbound.payload))
			}
			if got := protocol.Deref(result.RequestID); got != requestID {
				t.Fatalf("workspace action request_id = %q, want %q; payload=%s", got, requestID, string(outbound.payload))
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for workspace action %s", action)
		}
	}
}

func expectSpawnResult(t *testing.T, client *wsClient, sessionID string, success bool) {
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
			return
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

func expectPaneStatus(t *testing.T, d *Daemon, workspaceID, paneID string, status workspacelayout.PaneStatus, errorContains string) {
	t.Helper()
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatalf("workspace layout %s not found", workspaceID)
	}
	for _, pane := range snapshot.Panes {
		if pane.PaneID != paneID {
			continue
		}
		if pane.Status != status {
			t.Fatalf("pane %s status = %q, want %q", paneID, pane.Status, status)
		}
		if errorContains != "" && !strings.Contains(pane.Error, errorContains) {
			t.Fatalf("pane %s error = %q, want containing %q", paneID, pane.Error, errorContains)
		}
		return
	}
	t.Fatalf("pane %s not found in workspace %s", paneID, workspaceID)
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
