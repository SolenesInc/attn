package daemon_test

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSharedCodexNativeNamesFollowHiddenOwnersAndBothRenameDirections(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	a := w.Spawn(app, fakeagent.Codex, w.Path("same"))
	agentA := w.Launched(a)
	awaitSharedView(app, a, a)
	app.TypeLine(a, "first prompt")
	agentA.Prompted()
	agentA.GenerateNativeName("Generated A")
	awaitLabel(app, a, "Generated A")
	agentA.Reply("done <!-- attn:state=idle -->")
	answerTurnVerdict(t, w, "DONE")
	b := w.Spawn(app, fakeagent.Codex, w.Path("same"))
	agentB := w.Launched(b)
	awaitSharedView(app, b, b)
	app.TypeLine(b, "B prompt")
	agentB.Prompted()
	app.TypeLine(b, "/agents "+agentA.ConversationID)
	awaitSharedView(app, b, a)
	agentB.GenerateNativeName("Late generated B")
	awaitLabel(app, b, "Late generated B")
	if got := w.sessionLabel(a); got != "Generated A" {
		t.Fatal(got)
	}
	if err := cli.RenameSession(b, "Attn named hidden B"); err != nil {
		t.Fatal(err)
	}
	awaitLabel(app, b, "Attn named hidden B")
	if got := agentB.ReadNativeName(); got != "Attn named hidden B" {
		t.Fatal(got)
	}
	agentB.NativeName("Native named hidden B")
	awaitLabel(app, b, "Native named hidden B")
	shown, err := cli.SessionShow(b)
	if err != nil || shown.Entry.Label != "Native named hidden B" {
		t.Fatalf("ledger name: %+v %v", shown, err)
	}
	app.TypeLine(b, "/utility")
	app.AwaitScreen(b, "Utility complete")
	list, err := cli.SessionList(client.SessionListOptions{})
	if err != nil || len(list.Entries) != 2 {
		t.Fatalf("unexpected utility owner: %+v %v", list, err)
	}
	tasks := testworld.Request(app, protocol.TaskListMessage{Cmd: protocol.CmdTaskList, RequestID: protocol.Ptr("native-names")}, protocol.EventTaskListResult, func(r protocol.TaskListResultMessage) bool { return r.RequestID == "native-names" })
	for _, task := range tasks.Tasks {
		if task.Kind == "session_title" {
			t.Fatalf("competing Attn title: %+v", task)
		}
	}
	agentB.Reply("done <!-- attn:state=idle -->")
	answerTurnVerdict(t, w, "DONE")
}

func TestSharedCodexExplicitNameIsSetBeforeFirstWorkAndDoesNotNameNativeNew(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	sharedCodexSetting(t, app, true)
	a := w.Spawn(app, fakeagent.Codex, w.Path("same"), func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("same")
		m.InitialPrompt = protocol.Ptr("initial work")
	})
	agentA := w.Launched(a)
	agentA.Prompted()
	if got := agentA.ReadNativeName(); got != "same" {
		t.Fatalf("first work saw name %q", got)
	}
	agentA.GenerateNativeName("would replace explicit name")
	agentA.NativeName("Native manual name")
	awaitLabel(app, a, "Native manual name")
	agentA.Reply("done <!-- attn:state=idle -->")
	app.TypeLine(a, "/new")
	changed := testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, p := range e.WorkspaceLayout.Panes {
			if protocol.Deref(p.RuntimeID) == a && protocol.Deref(p.SessionID) != "" && protocol.Deref(p.SessionID) != a && protocol.Deref(p.CodexResolution) == protocol.CodexViewResolutionResolved {
				return true
			}
		}
		return false
	})
	var b string
	for _, p := range changed.WorkspaceLayout.Panes {
		if protocol.Deref(p.RuntimeID) == a {
			b = protocol.Deref(p.SessionID)
		}
	}
	agentB := w.Launched(b)
	if got := agentB.ReadNativeName(); got != "" {
		t.Fatalf("New inherited explicit name %q", got)
	}
	agentB.GenerateNativeName("Generated New")
	awaitLabel(app, b, "Generated New")
}

func TestSharedCodexFailedRenameRetainsConfirmedNameAndRestartReconcilesNativeName(t *testing.T) {
	w := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	w.Start()
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	a := w.Spawn(app, fakeagent.Codex, w.Path("same"), func(m *protocol.SpawnSessionMessage) { m.Label = protocol.Ptr("Launch name") })
	agentA := w.Launched(a)
	awaitSharedView(app, a, a)
	agentA.NativeName("Confirmed native name")
	awaitLabel(app, a, "Confirmed native name")
	agentA.RejectNativeNameWrites(true)
	result := renameFromApp(app, protocol.RenameSessionMessage{Cmd: protocol.CmdRenameSession, SessionID: a, Label: "Rejected name"}, a)
	if result.Success || protocol.Deref(result.Error) == "" {
		t.Fatalf("rename error not visible: %+v", result)
	}
	shown, err := cli.SessionShow(a)
	if err != nil || shown.Entry.Label != "Confirmed native name" {
		t.Fatalf("failed rename changed ledger: %+v %v", shown, err)
	}
	agentA.RejectNativeNameWrites(false)
	w.Stop()
	w.Start()
	app, cli = w.App(), w.Client()
	shown, err = cli.SessionShow(a)
	if err != nil || shown.Entry.Label != "Confirmed native name" {
		t.Fatalf("restart name: %+v %v", shown, err)
	}
	if got := agentA.ReadNativeName(); got != "Confirmed native name" {
		t.Fatalf("restart reapplied launch name: %q", got)
	}
	if err := cli.RenameSession(a, "After restart"); err != nil {
		t.Fatal(err)
	}
	awaitLabel(app, a, "After restart")
	if err := cli.Unregister(a); err != nil {
		t.Fatal(err)
	}
	awaitClosed(app, a)
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: a}); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: a}); err != nil {
		t.Fatal(err)
	}
	agentA.NativeName("Both views")
	awaitLabel(app, a, "Both views")
	if got := agentA.ReadNativeName(); got != "Both views" {
		t.Fatal(got)
	}
}

func TestSharedCodexNewestNameSurvivesBindingAndAnOlderResumeSnapshot(t *testing.T) {
	t.Setenv("ATTN_FAKE_CODEX_NAME_BEFORE_BIND", "Named before bind")
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	sharedCodexSetting(t, app, true)
	a := w.Spawn(app, fakeagent.Codex, w.Path("same"))
	agentA := w.Launched(a)
	awaitSharedView(app, a, a)
	awaitLabel(app, a, "Named before bind")
	agentA.NativeNameDuringResume("Renamed during resume")
	app.TypeLine(a, "/agents "+agentA.ConversationID)
	awaitLabel(app, a, "Renamed during resume")
	app.AwaitScreen(a, "Showing "+agentA.ConversationID)
	if got := w.sessionLabel(a); got != "Renamed during resume" {
		t.Fatalf("stale snapshot won: %q", got)
	}
}

func TestSharedCodexCrewNameIsNativeBeforeItsOpeningPrompt(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	cwd := w.Path("trellis-work")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}
	setCrew(t, cli, "trellis", protocol.CrewSetMessage{Cwd: protocol.Ptr(cwd)})
	wake := wakeCrew(t, cli, "trellis", "codex")
	agent := w.Launched(wake.SessionID)
	agent.Prompted()
	if got := agent.ReadNativeName(); got != "Trellis" {
		t.Fatalf("crew prompt saw native name %q", got)
	}
	agent.GenerateNativeName("Generated crew title")
	if got := agent.ReadNativeName(); got != "Trellis" {
		t.Fatal(got)
	}
	agent.Reply("ready <!-- attn:state=idle -->")
}
