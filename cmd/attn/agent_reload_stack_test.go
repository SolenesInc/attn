package main_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAgentReloadCLIUsesThePaletteLifecycleAndReportsBatchFailures(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	caller := s.Spawn(app, fakeagent.Claude, s.Path("caller"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) })
	s.Launched(caller)
	target := s.Spawn(app, fakeagent.Claude, s.Path("target"), func(m *protocol.SpawnSessionMessage) {
		m.Model = protocol.Ptr("claude-sonnet-5")
		m.Effort = protocol.Ptr("high")
		m.YoloMode = protocol.Ptr(true)
	})
	first := s.Launched(target)
	app.TypeLine(target, "save this conversation")
	first.Prompted()
	first.Reply("Saved. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, target, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	run := func(args ...string) testworld.Result {
		return s.Run(testworld.Invocation{Session: caller, Args: append([]string{"agent", "reload"}, args...)})
	}
	help := run("--help")
	if help.Code != 0 || !strings.Contains(help.Stdout, "non-persistent state may be lost") || !strings.Contains(help.Stdout, "reload --all") {
		t.Fatalf("reload help = %+v", help)
	}
	for _, args := range [][]string{nil, {target, "--all"}, {target, "--unknown"}, {""}} {
		got := run(args...)
		if got.Code != 2 {
			t.Fatalf("reload %q exited %d: %s", args, got.Code, got.Stderr)
		}
	}
	if got := s.Attn("agent", "reload", target); got.Code != 2 || !strings.Contains(got.Stderr, "no caller") {
		t.Fatalf("reload without a caller = %+v", got)
	}
	jsonFailure := run("missing", "--json")
	var failures protocol.AgentReloadResult
	if err := json.Unmarshal([]byte(jsonFailure.Stdout), &failures); err != nil || jsonFailure.Code != 1 || len(failures.Outcomes) != 1 || failures.Outcomes[0].Success {
		t.Fatalf("reload --json failure = %+v, %v", jsonFailure, err)
	}
	got := run(target[:8], "missing", target)
	t.Logf("reload <prefix> missing <duplicate>: exit %d\n%s%s", got.Code, got.Stdout, got.Stderr)
	if got.Code != 1 || strings.Count(got.Stdout, "reloaded session "+target+"\n") != 1 || !strings.Contains(got.Stderr, "missing") {
		t.Fatalf("reload batch = %+v", got)
	}
	second := s.Launched(target)
	if !second.Resumed || second.ConversationID != first.ConversationID || !slices.Contains(second.Argv, "claude-sonnet-5") || !slices.Contains(second.Argv, "high") || !slices.Contains(second.Argv, "--dangerously-skip-permissions") {
		t.Fatalf("reload ran %q resuming %v %s; want the same conversation %s, model, effort and permission mode", second.Argv, second.Resumed, second.ConversationID, first.ConversationID)
	}
	terminal := app.Terminal(target)
	testworld.Await(app, protocol.EventRuntimeRespawned, func(e protocol.WebSocketEvent) bool { return protocol.Deref(e.ID) == terminal })
	second.Exit(143)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == target })
	got = s.Attn("agent", "reload", target, "--source-session", caller, "--json")
	var result protocol.AgentReloadResult
	got.JSON(t, &result)
	t.Logf("reload exited target --source-session <chief> --json: %s", strings.TrimSpace(got.Stdout))
	if len(result.Outcomes) != 1 || !result.Outcomes[0].Success || protocol.Deref(result.Outcomes[0].SessionID) != protocol.SessionID(target) {
		t.Fatalf("reload --json = %+v", result)
	}
	third := s.Launched(target)
	if !third.Resumed || third.ConversationID != first.ConversationID {
		t.Fatalf("exited reload resumed %v %s, want %s", third.Resumed, third.ConversationID, first.ConversationID)
	}
	peek, err := s.Client().AgentPeek(target)
	if err != nil || peek.Screen == nil || peek.Screen.Cols != 80 || peek.Screen.Rows != 24 {
		t.Fatalf("exited reload screen = %+v, %v; want 80x24", peek, err)
	}
	got = run("--all", "--json")
	got.JSON(t, &result)
	if len(result.Outcomes) != 2 || !result.Outcomes[0].Success || !result.Outcomes[1].Success || protocol.Deref(result.Outcomes[1].SessionID) != protocol.SessionID(caller) {
		t.Fatalf("reload --all = %+v", result)
	}
	s.Launched(target)
	s.Launched(caller)
}

func TestAgentReloadContinuesAfterARelaunchFailure(t *testing.T) {
	w := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, fakeagent.Pi))
	w.Start()
	app, cli := w.App(), w.Client()
	if app.Initial.Settings["pi_available"] != "true" {
		testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["pi_available"] == "true" })
	}
	chief := w.Spawn(app, fakeagent.Claude, w.Path("chief"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) })
	w.Launched(chief)
	plugin := w.Spawn(app, fakeagent.Pi, w.Path("plugin"))
	first := w.Launched(plugin)
	worker := w.Spawn(app, fakeagent.Claude, w.Path("worker"))
	w.Launched(worker)
	allow := w.RefusePiLaunches("the pi session store is locked")
	result, err := cli.AgentReload([]string{plugin, worker}, protocol.SessionID(chief), false)
	allow()
	if err != nil || len(result.Outcomes) != 2 || result.Outcomes[0].Success || !strings.Contains(protocol.Deref(result.Outcomes[0].Error), "store is locked") || !result.Outcomes[1].Success {
		t.Fatalf("reload batch = %+v, %v; want driver refusal then success", result, err)
	}
	w.Launched(worker)
	app.TypeLine(plugin, "the failed reload kept me running")
	if got := first.Prompted(); got != "the failed reload kept me running" {
		t.Fatalf("preserved plugin received %q", got)
	}
	first.Reply("Still here. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, worker, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}
