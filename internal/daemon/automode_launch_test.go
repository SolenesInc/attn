package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

func TestReloadCarriesThePromotedAutoModeConfig(t *testing.T) {
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

	now := time.Now().UTC()
	proposal, err := d.store.CreateAutoModeProposal(
		automode.KindHost, "", `{"host":"crates.io","decision":"allow"}`, "", now)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, _, err := d.store.PromoteAutoModeProposal(proposal.ID, now); err != nil {
		t.Fatalf("promote: %v", err)
	}

	plugin, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = plugin.Close()
		<-done
	}()
	registerTestPluginDriver(t, plugin, "snipe", map[string]bool{
		"resume": true, "launch_instructions": true, "auto_mode": true,
	})

	resumed := make(chan struct{})
	go func() {
		defer close(resumed)
		request := decodeJSONRPCMessage(t, plugin)
		if request.Method != "driver.resume" {
			t.Errorf("method = %q, want driver.resume", request.Method)
			return
		}
		var params pluginDriverSpawnParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Errorf("decode resume params: %v", err)
			return
		}
		if params.AutoMode == nil {
			t.Error("resume params carry no auto mode config")
		} else if allowed := params.AutoMode.Network.AllowedDomains; len(allowed) != 1 ||
			allowed[0] != "crates.io" {
			t.Errorf("auto mode allowed domains = %v, want the promoted host", allowed)
		} else if denied := automode.StripShippedNetwork(config.WSPort(), params.AutoMode.Network); len(denied.DeniedDomains) != 0 {
			t.Errorf("auto mode denied domains = %v, want only the shipped ones",
				params.AutoMode.Network.DeniedDomains)
		}
		respondPluginRequest(t, plugin, request, pluginDriverSpawnResult{Argv: []string{"snipe"}})
	}()

	d.reloadSessionAgent("snipe-session")

	select {
	case <-resumed:
	case <-time.After(2 * time.Second):
		t.Fatal("driver.resume was never requested")
	}
}

func TestSpawnOmitsAutoModeForADriverThatDoesNotAskForIt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "snipe", map[string]bool{"launch_instructions": true})

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		request := decodeJSONRPCMessage(t, client)
		var params pluginDriverSpawnParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Errorf("decode spawn params: %v", err)
			return
		}
		if params.AutoMode != nil {
			t.Errorf("a driver without the auto_mode capability was handed %+v", params.AutoMode)
		}
		respondPluginRequest(t, client, request, pluginDriverSpawnResult{Argv: []string{"snipe"}})
	}()

	addTestWorkspace(d, "workspace-snipe", t.TempDir())
	ws := &wsClient{send: make(chan outboundMessage, 2), attachedStreams: make(map[string]ptybackend.Stream)}
	d.handleSpawnSession(ws, &protocol.SpawnSessionMessage{
		ID:          "snipe-session",
		Cwd:         t.TempDir(),
		WorkspaceID: "workspace-snipe",
		Agent:       "snipe",
		Cols:        80,
		Rows:        24,
	})
	<-requestDone
}

func TestReloadKeepsThePerSessionAutoModeOverride(t *testing.T) {
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
	if _, err := d.store.SetAutoModeEnabledDefault(true, time.Now().UTC()); err != nil {
		t.Fatalf("set default: %v", err)
	}
	d.store.SetLaunchIntent("snipe-session", store.LaunchIntent{AutoMode: protocol.Ptr(false)})

	plugin, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = plugin.Close()
		<-done
	}()
	registerTestPluginDriver(t, plugin, "snipe", map[string]bool{
		"resume": true, "launch_instructions": true, "auto_mode": true,
	})

	resumed := make(chan struct{})
	go func() {
		defer close(resumed)
		request := decodeJSONRPCMessage(t, plugin)
		var params pluginDriverSpawnParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Errorf("decode resume params: %v", err)
			return
		}
		if params.AutoMode == nil {
			t.Error("resume params carry no auto mode config")
		} else if params.AutoMode.EnabledDefault {
			t.Error("the reload picked up enabled_default instead of the session's override")
		}
		respondPluginRequest(t, plugin, request, pluginDriverSpawnResult{Argv: []string{"snipe"}})
	}()

	d.reloadSessionAgent("snipe-session")

	select {
	case <-resumed:
	case <-time.After(2 * time.Second):
		t.Fatal("driver.resume was never requested")
	}

	intent, ok := d.store.LaunchIntent("snipe-session")
	if !ok {
		t.Fatal("the reload dropped the launch intent")
	}
	if intent.AutoMode == nil || *intent.AutoMode {
		t.Errorf("the reload rewrote the intent and lost the override: %v", intent.AutoMode)
	}
}
