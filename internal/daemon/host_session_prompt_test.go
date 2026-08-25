package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const conversationBrief = "Fix the flaky test in internal/store.\nReport on the ticket when it is green."

func serveOneDriverSpawn(t *testing.T, conn net.Conn, argv []string) <-chan pluginDriverSpawnParams {
	t.Helper()
	spawned := make(chan pluginDriverSpawnParams, 1)
	go func() {
		for {
			request := decodeJSONRPCMessage(t, conn)
			if request.Method == pluginHealthMethod {
				respondPluginRequest(t, conn, request, pluginHealthResult{OK: true})
				continue
			}
			if request.Method != "driver.spawn" {
				respondPluginRequest(t, conn, request, map[string]any{"ok": true})
				continue
			}
			var params pluginDriverSpawnParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Errorf("decode driver.spawn params: %v", err)
				return
			}
			spawned <- params
			respondPluginRequest(t, conn, request, pluginDriverSpawnResult{Argv: argv})
			return
		}
	}()
	return spawned
}

func idleHostCommand(t *testing.T) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "idle-host.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nwhile IFS= read -r line; do :; done\n"), 0o755); err != nil {
		t.Fatalf("write idle host: %v", err)
	}
	return []string{path}
}

func TestConversationLaunchPromptReachesTheDriverAndOutlivesItsHost(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	workspaceID, _, cwd := setupDelegationSource(t, d, backend)
	pipe, done := startPluginPipe(t, d, "pi-fixture-plugin", nil)
	defer func() {
		_ = pipe.Close()
		<-done
	}()
	registerTestPluginDriver(t, pipe, "pi-fixture", map[string]bool{
		pluginDriverConversationCapability: true,
		"initial_prompt":                   true,
		"state_reporting":                  true,
	})
	spawned := serveOneDriverSpawn(t, pipe, idleHostCommand(t))

	const sessionID = "conv-brief"
	client := newWorkspaceProtocolTestClient()
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:           protocol.CmdSpawnSession,
		ID:            sessionID,
		Cwd:           cwd,
		WorkspaceID:   workspaceID,
		Agent:         "pi-fixture",
		Cols:          80,
		Rows:          24,
		InitialPrompt: protocol.Ptr(conversationBrief),
	})
	expectSpawnResult(t, client, sessionID, true)
	t.Cleanup(func() { _ = d.ensureHostSessions().Kill(sessionID) })

	select {
	case params := <-spawned:
		if params.InitialPrompt != conversationBrief {
			t.Fatalf("driver.spawn initial_prompt = %q, want the brief", params.InitialPrompt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the driver was never asked to spawn")
	}

	intent, ok := d.store.LaunchIntent(sessionID)
	if !ok {
		t.Fatal("a conversation session spawned with no stored launch intent")
	}
	if intent.InitialPrompt != conversationBrief {
		t.Fatalf("stored launch intent prompt = %q, want the brief", intent.InitialPrompt)
	}
	relaunch, _ := buildStoredIntentSpawn(d.store.Get(sessionID), intent, 80, 24)
	if protocol.Deref(relaunch.InitialPrompt) != conversationBrief {
		t.Fatalf("relaunch prompt = %q, want the brief", protocol.Deref(relaunch.InitialPrompt))
	}
}

func TestPTYLaunchPromptIsNotStoredForReplay(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	workspaceID, _, cwd := setupDelegationSource(t, d, backend)

	const sessionID = "pty-brief"
	client := newWorkspaceProtocolTestClient()
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:           protocol.CmdSpawnSession,
		ID:            sessionID,
		Cwd:           cwd,
		WorkspaceID:   workspaceID,
		Agent:         string(protocol.SessionAgentClaude),
		Cols:          80,
		Rows:          24,
		InitialPrompt: protocol.Ptr(conversationBrief),
	})
	expectSpawnResult(t, client, sessionID, true)

	spawn, ok := backend.LastSpawn()
	if !ok || spawn.InitialPromptFile == "" {
		t.Fatalf("a PTY agent's brief did not reach it as a prompt file: %+v", spawn)
	}
	_ = os.Remove(spawn.InitialPromptFile)

	intent, ok := d.store.LaunchIntent(sessionID)
	if !ok {
		t.Fatal("no stored launch intent for the PTY session")
	}
	if intent.InitialPrompt != "" {
		t.Fatalf("a PTY launch stored a replayable prompt %q; a reload would re-run it", intent.InitialPrompt)
	}
	relaunch, _ := buildStoredIntentSpawn(d.store.Get(sessionID), intent, 80, 24)
	if relaunch.InitialPrompt != nil {
		t.Fatalf("relaunch carried a prompt %q for a resumed PTY session", protocol.Deref(relaunch.InitialPrompt))
	}
}
