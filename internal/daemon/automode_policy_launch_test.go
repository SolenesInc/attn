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

func TestSpawnAppliesThePerSessionPolicyPair(t *testing.T) {
	for _, tc := range []struct {
		name        string
		policy      string
		sandbox     string
		wantPolicy  string
		wantSandbox string
	}{
		{
			name:   "both halves override the daemon default",
			policy: automode.PolicyUntrusted, sandbox: automode.SandboxReadOnly,
			wantPolicy: automode.PolicyUntrusted, wantSandbox: automode.SandboxReadOnly,
		},
		{
			name:       "neither half follows the daemon default",
			wantPolicy: automode.PolicyOnRequest, wantSandbox: automode.SandboxWorkspaceWrite,
		},
		{
			name:       "the approval policy alone leaves the sandbox on the default",
			policy:     automode.PolicyNever,
			wantPolicy: automode.PolicyNever, wantSandbox: automode.SandboxWorkspaceWrite,
		},
		{
			name:       "the sandbox mode alone leaves the policy on the default",
			sandbox:    automode.SandboxDangerFullAccess,
			wantPolicy: automode.PolicyOnRequest, wantSandbox: automode.SandboxDangerFullAccess,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			d.ptyBackend = &fakeSpawnBackend{}
			setTestAutoModePolicy(t, d, automode.PolicyOnRequest, automode.SandboxWorkspaceWrite)
			client := registerPolicyTestDriver(t, d, map[string]bool{
				"launch_instructions": true, "auto_mode": true,
			})
			captured := capturePolicyTestSpawn(t, client)

			addTestWorkspace(d, "workspace-snipe", t.TempDir())
			ws := &wsClient{send: make(chan outboundMessage, 2), attachedStreams: make(map[string]ptybackend.Stream)}
			msg := &protocol.SpawnSessionMessage{
				ID:          "snipe-session",
				Cwd:         t.TempDir(),
				WorkspaceID: "workspace-snipe",
				Agent:       "snipe",
				Cols:        80,
				Rows:        24,
			}
			if tc.policy != "" {
				msg.ApprovalPolicy = protocol.Ptr(tc.policy)
			}
			if tc.sandbox != "" {
				msg.SandboxMode = protocol.Ptr(tc.sandbox)
			}
			d.handleSpawnSession(ws, msg)
			assertPolicyPair(t, awaitPolicyTestSpawn(t, captured), tc.wantPolicy, tc.wantSandbox)

			intent, ok := d.store.LaunchIntent("snipe-session")
			if !ok {
				t.Fatal("no launch intent was persisted")
			}
			if intent.ApprovalPolicy != tc.policy || intent.SandboxMode != tc.sandbox {
				t.Errorf("intent pair = %q/%q, want %q/%q",
					intent.ApprovalPolicy, intent.SandboxMode, tc.policy, tc.sandbox)
			}
			session := &protocol.Session{
				ID: "snipe-session", Directory: t.TempDir(), Agent: "snipe", WorkspaceID: "workspace-snipe",
			}
			revived, _ := buildStoredIntentSpawn(session, intent, 80, 24)
			if got := protocol.Deref(revived.ApprovalPolicy); got != tc.policy {
				t.Errorf("revive spawn approval policy = %q, want %q", got, tc.policy)
			}
			if got := protocol.Deref(revived.SandboxMode); got != tc.sandbox {
				t.Errorf("revive spawn sandbox mode = %q, want %q", got, tc.sandbox)
			}
		})
	}
}

func TestReloadKeepsThePerSessionPolicyPair(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"snipe-session"},
		info:    ptybackend.SessionInfo{Cols: 100, Rows: 32},
		params:  ptybackend.SessionLaunchParams{Recorded: true},
	}
	d := newReloadTestDaemon(t, backend)
	addTestWorkspace(d, "ws-snipe-session", t.TempDir())
	addReloadSession(d, "snipe-session", protocol.SessionAgent("snipe"), protocol.SessionStateIdle)
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

func TestYoloOnAnAutoModeDriverLaunchesWithFullAccess(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	setTestAutoModePolicy(t, d, automode.PolicyOnRequest, automode.SandboxWorkspaceWrite)
	client := registerPolicyTestDriver(t, d, map[string]bool{
		"launch_instructions": true, "auto_mode": true,
	})
	captured := capturePolicyTestSpawn(t, client)

	addTestWorkspace(d, "workspace-snipe", t.TempDir())
	ws := &wsClient{send: make(chan outboundMessage, 2), attachedStreams: make(map[string]ptybackend.Stream)}
	d.handleSpawnSession(ws, &protocol.SpawnSessionMessage{
		ID:             "snipe-session",
		Cwd:            t.TempDir(),
		WorkspaceID:    "workspace-snipe",
		Agent:          "snipe",
		Cols:           80,
		Rows:           24,
		YoloMode:       protocol.Ptr(true),
		ApprovalPolicy: protocol.Ptr(automode.PolicyUntrusted),
		SandboxMode:    protocol.Ptr(automode.SandboxReadOnly),
	})
	params := awaitPolicyTestSpawn(t, captured)
	assertPolicyPair(t, params, automode.PolicyNever, automode.SandboxDangerFullAccess)
	if params.Yolo {
		t.Error("a driver that never advertised yolo was handed the flag")
	}

	intent, ok := d.store.LaunchIntent("snipe-session")
	if !ok {
		t.Fatal("no launch intent was persisted")
	}
	if intent.ApprovalPolicy != automode.PolicyNever || intent.SandboxMode != automode.SandboxDangerFullAccess {
		t.Errorf("intent pair = %q/%q, want the full-access pair",
			intent.ApprovalPolicy, intent.SandboxMode)
	}
	if !intent.YoloMode {
		t.Error("the intent lost the yolo launch")
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

func TestSpawnRefusesAPolicyPairItCannotHonour(t *testing.T) {
	for _, tc := range []struct {
		name         string
		autoMode     bool
		policy       string
		sandbox      string
		wantFragment []string
	}{
		{
			name: "an unknown approval policy", autoMode: true, policy: "sometimes",
			wantFragment: []string{`approval policy "sometimes" is not one of`, "untrusted, on-request, never"},
		},
		{
			name: "an unknown sandbox mode", autoMode: true, sandbox: "read-write",
			wantFragment: []string{`sandbox mode "read-write" is not one of`, "read-only, workspace-write, danger-full-access"},
		},
		{
			name: "an agent that reads no auto mode", policy: automode.PolicyNever,
			wantFragment: []string{`agent "snipe" does not support a per-session approval policy or sandbox mode`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			d.ptyBackend = &fakeSpawnBackend{}
			capabilities := map[string]bool{"launch_instructions": true}
			if tc.autoMode {
				capabilities["auto_mode"] = true
			}
			registerPolicyTestDriver(t, d, capabilities)
			addTestWorkspace(d, "workspace-snipe", t.TempDir())

			msg := &protocol.SpawnSessionMessage{
				ID:          "snipe-session",
				Cwd:         t.TempDir(),
				WorkspaceID: "workspace-snipe",
				Agent:       "snipe",
				Cols:        80,
				Rows:        24,
			}
			if tc.policy != "" {
				msg.ApprovalPolicy = protocol.Ptr(tc.policy)
			}
			if tc.sandbox != "" {
				msg.SandboxMode = protocol.Ptr(tc.sandbox)
			}
			rejection := d.runSpawnPipeline(msg, internalSpawnPolicy{})
			if rejection == nil {
				t.Fatal("the spawn was accepted, want a refusal naming the value and the allowed set")
			}
			got := rejection.reason().Error()
			for _, want := range tc.wantFragment {
				if !strings.Contains(got, want) {
					t.Errorf("rejection = %q, want it to name %q", got, want)
				}
			}
			if d.store.Get("snipe-session") != nil {
				t.Error("the refused spawn left a session behind")
			}
		})
	}
}
