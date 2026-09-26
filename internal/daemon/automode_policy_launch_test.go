package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

func setTestAutoModePolicy(t *testing.T, d *Daemon, policy, sandbox string) {
	t.Helper()
	if _, err := d.store.SetAutoModePolicy(automode.PolicyAmendment{
		ApprovalPolicy: protocol.Ptr(policy),
		SandboxMode:    protocol.Ptr(sandbox),
	}, time.Now().UTC()); err != nil {
		t.Fatalf("set the daemon policy: %v", err)
	}
}

func registerPolicyTestDriver(t *testing.T, d *Daemon, capabilities map[string]bool) net.Conn {
	t.Helper()
	client, done := startPluginPipe(t, d, "snipe-plugin", nil)
	t.Cleanup(func() {
		_ = client.Close()
		<-done
	})
	registerTestPluginDriver(t, client, "snipe", capabilities)
	return client
}

func capturePolicyTestSpawn(t *testing.T, client net.Conn) <-chan pluginDriverSpawnParams {
	t.Helper()
	captured := make(chan pluginDriverSpawnParams, 1)
	go func() {
		request := decodeJSONRPCMessage(t, client)
		var params pluginDriverSpawnParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Errorf("decode spawn params: %v", err)
			close(captured)
			return
		}
		captured <- params
		respondPluginRequest(t, client, request, pluginDriverSpawnResult{Argv: []string{"snipe"}})
	}()
	return captured
}

func awaitPolicyTestSpawn(t *testing.T, captured <-chan pluginDriverSpawnParams) pluginDriverSpawnParams {
	t.Helper()
	select {
	case params, ok := <-captured:
		if !ok {
			t.Fatal("the driver spawn params never decoded")
		}
		return params
	case <-time.After(2 * time.Second):
		t.Fatal("the driver was never asked to spawn")
	}
	return pluginDriverSpawnParams{}
}

func assertPolicyPair(t *testing.T, params pluginDriverSpawnParams, policy, sandbox string) {
	t.Helper()
	if params.AutoMode == nil {
		t.Fatal("spawn params carry no auto mode config")
	}
	if params.AutoMode.ApprovalPolicy != policy || params.AutoMode.SandboxMode != sandbox {
		t.Errorf("auto mode pair = %q/%q, want %q/%q",
			params.AutoMode.ApprovalPolicy, params.AutoMode.SandboxMode, policy, sandbox)
	}
}

func TestReloadKeepsThePerSessionPolicyPair(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"snipe-session"},
		info:    ptybackend.SessionInfo{Cols: 100, Rows: 32},
		params:  ptybackend.SessionLaunchParams{Recorded: true},
	}
	d := newReloadTestDaemon(t, backend)
	directory := t.TempDir()
	addTestWorkspace(d, "ws-snipe-session", directory)
	addReloadSessionAt(d, "snipe-session", protocol.SessionAgent("snipe"), protocol.SessionStateIdle, directory)
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	setTestAutoModePolicy(t, d, automode.PolicyOnRequest, automode.SandboxWorkspaceWrite)
	d.store.SetLaunchIntent("snipe-session", store.LaunchIntent{
		ApprovalPolicy: automode.PolicyUntrusted,
		SandboxMode:    automode.SandboxReadOnly,
	})

	client := registerPolicyTestDriver(t, d, map[string]bool{
		"resume": true, "launch_instructions": true, "auto_mode": true,
	})
	captured := capturePolicyTestSpawn(t, client)

	d.reloadSessionAgent("snipe-session")
	assertPolicyPair(t, awaitPolicyTestSpawn(t, captured), automode.PolicyUntrusted, automode.SandboxReadOnly)

	intent, ok := d.store.LaunchIntent("snipe-session")
	if !ok {
		t.Fatal("the reload dropped the launch intent")
	}
	if intent.ApprovalPolicy != automode.PolicyUntrusted || intent.SandboxMode != automode.SandboxReadOnly {
		t.Errorf("the reload rewrote the intent and lost the pair: %q/%q",
			intent.ApprovalPolicy, intent.SandboxMode)
	}
}

func TestYoloIsStillRefusedForADriverThatReadsNeitherFlag(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	registerPolicyTestDriver(t, d, map[string]bool{"launch_instructions": true})
	addTestWorkspace(d, "workspace-snipe", t.TempDir())

	rejection := d.runSpawnPipeline(&protocol.SpawnSessionMessage{
		ID:          "snipe-session",
		Cwd:         t.TempDir(),
		WorkspaceID: "workspace-snipe",
		Agent:       "snipe",
		Cols:        80,
		Rows:        24,
		YoloMode:    protocol.Ptr(true),
	}, internalSpawnPolicy{})
	if rejection == nil {
		t.Fatal("a yolo launch was accepted by a driver that supports neither yolo nor auto mode")
	}
	if got := rejection.reason().Error(); !strings.Contains(got, "does not support yolo launches") {
		t.Errorf("rejection = %q, want the yolo refusal", got)
	}
}
