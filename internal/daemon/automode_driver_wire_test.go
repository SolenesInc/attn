package daemon_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type autoModeDriverSpawn struct {
	SessionID string          `json:"session_id"`
	RunID     string          `json:"run_id"`
	AutoMode  json.RawMessage `json:"auto_mode"`
}

func registerAutoModeDriver(t *testing.T, w *world, app *testworld.Peer, agent string, capabilities map[string]bool) *pluginPeer {
	t.Helper()
	driver := connectPlugin(t, w, agent+"-plugin")
	driver.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "driver.register", "params": map[string]any{"agent": agent, "capabilities": capabilities}})
	if answer := driver.read(); answer.Error != nil {
		t.Fatalf("the daemon refused driver %s: %s", agent, answer.Error.Message)
	}
	awaitAgentAvailable(app, agent)
	return driver
}

func spawnOnAutoModeDriver(t *testing.T, w *world, app *testworld.Peer, driver *pluginPeer, agent, label string) autoModeDriverSpawn {
	t.Helper()
	cwd := w.Path(agent + "-work")
	session := uuid.NewString()
	workspace := registerWorkspace(t, app, cwd)
	testworld.Request(app, protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: workspace, SessionID: session, PaneID: protocol.Ptr("pane-" + session),
	}, protocol.EventWorkspaceLayoutActionResult, func(protocol.WorkspaceLayoutActionResultMessage) bool { return true })
	app.Send(protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: session, Agent: agent, Cwd: cwd, WorkspaceID: workspace, Label: protocol.Ptr(label), Cols: 80, Rows: 24,
	})
	var spawn autoModeDriverSpawn
	id := driver.expect("driver.spawn", &spawn)
	driver.answer(id, map[string]any{"argv": []string{"/bin/cat"}})
	if result := testworld.Await(app, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == session }); !result.Success {
		t.Fatalf("spawning on driver %s: %s", agent, protocol.Deref(result.Error))
	}
	return spawn
}

func registerWorkspace(t *testing.T, app *testworld.Peer, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "workspace-" + uuid.NewString()
	testworld.Request(app, protocol.RegisterWorkspaceMessage{Cmd: protocol.CmdRegisterWorkspace, ID: id, Title: "work", Directory: dir},
		protocol.EventWorkspaceRegistered, func(protocol.WebSocketEvent) bool { return true })
	return id
}

func (p *pluginPeer) report(method string, params map[string]any) *pluginWireError {
	p.t.Helper()
	id := uuid.NewString()
	p.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	for {
		message := p.read()
		if message.Method != "" {
			continue
		}
		var answered string
		if json.Unmarshal(message.ID, &answered) == nil && answered == id {
			return message.Error
		}
	}
}

func TestADriverHearsTheAutoModeConfigOnlyWhenItAsksForIt(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	asking := registerAutoModeDriver(t, w, app, "snipe", map[string]bool{"auto_mode": true})
	silent := registerAutoModeDriver(t, w, app, "quill", map[string]bool{})

	if spawn := spawnOnAutoModeDriver(t, w, app, asking, "snipe", "asking"); len(spawn.AutoMode) == 0 || string(spawn.AutoMode) == "null" {
		t.Error("a driver with the auto_mode capability was launched without the auto mode config")
	}
	if spawn := spawnOnAutoModeDriver(t, w, app, silent, "quill", "silent"); len(spawn.AutoMode) != 0 {
		t.Errorf("a driver without the auto_mode capability was handed %s", spawn.AutoMode)
	}
}

func TestADriverReportsDenialsAndAmendmentsOnlyForTheRunItOwns(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	driver := registerAutoModeDriver(t, w, app, "snipe", map[string]bool{"auto_mode": true})
	run := spawnOnAutoModeDriver(t, w, app, driver, "snipe", "sunny otter")
	for _, stale := range []string{"run-other", ""} {
		for method, params := range map[string]map[string]any{
			"session.report_automode_denial":      {"action": "bash: git push --force", "reason": "rewrites history", "rule": "classifier-2a"},
			"session.report_execpolicy_amendment": {"pattern": []string{"cargo", "build"}, "decision": automode.DecisionAllow},
			"session.report_network_amendment":    {"host": "crates.io", "decision": automode.HostAllow},
		} {
			params["session_id"], params["run_id"] = run.SessionID, stale
			if refused := driver.report(method, params); refused == nil || !strings.Contains(refused.Message, "does not own") {
				t.Errorf("%s for run %q = %+v, want it refused as not the driver's run", method, stale, refused)
			}
		}
	}
	if denials, err := cli.AutoModeDenials(10); err != nil || len(denials.Denials) != 0 {
		t.Fatalf("after refused reports the denial log holds %+v (%v), want nothing", denials, err)
	}
	if cfg := autoModeState(app).Config; len(userRuleLines(t, cfg)) != 0 || len(cfg.Network.AllowedDomains) != 0 {
		t.Fatalf("a refused amendment reached the config: rules %v, hosts %v", userRuleLines(t, cfg), cfg.Network.AllowedDomains)
	}

	for method, params := range map[string]map[string]any{
		"session.report_execpolicy_amendment": {"pattern": []string{"cargo", "build"}, "decision": automode.DecisionAllow},
		"session.report_network_amendment":    {"host": "crates.io", "decision": automode.HostAllow},
	} {
		params["session_id"], params["run_id"] = run.SessionID, run.RunID
		if refused := driver.report(method, params); refused != nil {
			t.Fatalf("%s for the driver's own run was refused: %s", method, refused.Message)
		}
	}
	state := autoModeState(app)
	if rules := userRuleLines(t, state.Config); len(rules) != 1 || rules[0] != "allow cargo build" {
		t.Errorf("rules beside the shipped ones = %v, want the reported rule in force", rules)
	}
	if hosts := state.Config.Network.AllowedDomains; len(hosts) != 1 || hosts[0] != "crates.io" {
		t.Errorf("allowed hosts = %v, want the reported host in force", hosts)
	}
	for _, proposal := range state.Proposals {
		t.Errorf("a reported amendment left %q for the user to promote", proposal.Summary)
	}
}

func TestADriverDenialIsAnnouncedNamingTheSessionWhatWasBlockedWhyAndWhoDecided(t *testing.T) {
	w := newWorld(t, fakeagent.Pi)
	app := w.App()
	awaitAgentAvailable(app, string(fakeagent.Pi))
	session := w.Spawn(app, fakeagent.Pi, w.Path("envelope"), func(m *protocol.SpawnSessionMessage) { m.Label = protocol.Ptr("envelope work") })
	w.Launched(session).Deny(fakeagent.Denial{Tool: "bash", Action: "bash: curl https://example.com", Reason: "the user never asked to reach that host", Rule: "classifier-2a"})

	var announced []protocol.Notification
	for _, n := range listNotifications(app).Notifications {
		if n.Kind == "automode_denied" {
			announced = append(announced, n)
		}
	}
	if len(announced) != 1 {
		t.Fatalf("the app was told of %d denials, want the one reported", len(announced))
	}
	note := announced[0]
	if note.SourceID != session || !strings.Contains(note.Title, "envelope work") || !strings.Contains(note.Body, "curl https://example.com") {
		t.Errorf("the denial notification reads %+v, want it on the session, titled with its label and naming the blocked call", note)
	}
	if detail := note.Detail; !strings.Contains(detail, "never asked to reach that host") || !strings.Contains(detail, "classifier-2a") {
		t.Errorf("the denial notification's detail is %q, want the reason and who decided", detail)
	}
}

func autoModeState(app *testworld.Peer) protocol.AutoModeStateResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	state := testworld.Request(app, protocol.AutoModeGetMessage{Cmd: protocol.CmdAutoModeGet, RequestID: requestID}, protocol.EventAutoModeStateResult,
		func(r protocol.AutoModeStateResultMessage) bool { return r.RequestID == requestID })
	if !state.Success {
		app.T.Fatalf("automode_get: %s", protocol.Deref(state.Error))
	}
	return state
}
