package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestOutpostAgentsRunOnTheirMachineAndReportToTheHome(t *testing.T) {
	for _, harness := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		t.Run(string(harness), func(t *testing.T) {
			home := testworld.NewStack(t)
			outpost := testworld.NewStack(t, testworld.WithAgents(harness))
			pair := testworld.PrepareOutpostPair(t, home, outpost)
			pair.Start()
			awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
			outpost.SetCodexFolderTrust(outpost.Path("agent"), "trusted")
			id := home.Spawn(pair.App, harness, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) {
				m.Host = protocol.Ptr(pair.Host)
			})
			agent := outpost.LaunchedCarrying(pair.App.Terminal(id))
			pair.App.TypeLine(id, "work on the outpost")
			if got := agent.Prompted(); got != "work on the outpost" {
				t.Fatalf("prompt = %q", got)
			}
			agent.Reply("Outpost ready. <!-- attn:state=waiting_input -->")
			session := testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool {
				return protocol.Deref(s.TurnOwed) && s.Usage != nil && s.Usage.TotalTokens > 0
			})
			if protocol.Deref(session.Host) != pair.Host {
				t.Fatalf("host = %q, want %q", protocol.Deref(session.Host), pair.Host)
			}
			pair.App.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: protocol.SessionID(id)})
			testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool { return !protocol.Deref(s.TurnOwed) })
			until := time.Now().Add(time.Hour)
			if err := home.Client().SnoozeSession("", protocol.SessionID(id), until); err != nil {
				t.Fatal(err)
			}
			testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool {
				return protocol.Deref(s.TurnSnoozedUntil) == until.UTC().Format(time.RFC3339Nano)
			})
			pair.App.Send(protocol.WakeTurnMessage{Cmd: protocol.CmdWakeTurn, SessionID: protocol.SessionID(id)})
			testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool {
				return protocol.Deref(s.TurnOwed) && protocol.Deref(s.TurnSnoozedUntil) == ""
			})
			rename := outpost.Run(testworld.Invocation{Terminal: pair.App.Terminal(id), Args: []string{"session", "rename", "Remote CLI label"}})
			if rename.Code != 0 {
				t.Fatalf("outpost agent CLI rename: %+v", rename)
			}
			testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool { return s.Label == "Remote CLI label" })
			t.Logf("%s terminal=%s host=%s conversation=%s state=%s tokens=%d", harness, pair.App.Terminal(id), pair.Host, agent.ConversationID, session.State, session.Usage.TotalTokens)
			t.Log("home settle, snooze and wake reached the machine; the agent's CLI rename reached the home")
		})
	}
}

func TestOutpostAgentsQueueMessagesOfflineAndRecoverAcrossBothRestarts(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	shareOutpostCodex(t, pair)
	app := pair.App
	outpost.SetCodexFolderTrust(outpost.Path("agent"), "trusted")
	id := home.Spawn(app, fakeagent.Codex, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	terminal := protocol.TerminalID(app.Terminal(id))
	agent := outpost.LaunchedCarrying(string(terminal))
	app.TypeLine(id, "before disconnect")
	agent.Prompted()
	agent.Reply("Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	url := "https://github.com/example/project/pull/1"
	if err := outpost.Client().RecordPullRequestCreatedFromTerminal(terminal, url); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return len(s.PullRequests) == 1 })
	if err := home.Client().ForgetSessionPullRequest(protocol.SessionID(id), url); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return len(s.PullRequests) == 0 })
	sender := home.Spawn(app, fakeagent.Harness("shell"), home.Path("sender"))
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"), "false")
	if err := home.Client().SnoozeSession("", protocol.SessionID(id), time.Now().Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "outpost is unreachable") {
		t.Fatalf("offline snooze = %v", err)
	}
	message, err := home.Client().AgentMsg(id, protocol.SessionID(sender), "continue when connected")
	if err != nil || message == nil || !strings.Contains(message.Detail, "outpost, which is unreachable") {
		t.Fatalf("offline message = %+v, %v", message, err)
	}
	t.Logf("offline message: %s", message.Detail)
	mark := len(app.Log())
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "true"), "true")
	outpostStateSince(app, "outpost", protocol.OutpostStateConnected, mark)
	if prompt := outpost.CodexServer().Prompted(agent.ConversationID); !strings.Contains(prompt, "attn") {
		t.Fatalf("queued inbox ring = %q", prompt)
	}
	outpost.CodexServer().Reply(agent.ConversationID, "Connected. <!-- attn:state=waiting_input -->")
	testworld.AwaitSessionSince(app, mark, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	for _, restart := range []struct {
		name  string
		stack *testworld.Stack
	}{{"home", home}, {"outpost", outpost}} {
		restart.stack.Stop()
		restart.stack.Start()
		app = home.App()
		outpostStateOnConnect(app, "outpost", protocol.OutpostStateConnected)
		app.TypeLine(id, "after "+restart.name+" restart")
		agent.Prompted()
		agent.Reply("Still current. <!-- attn:state=waiting_input -->")
		current := testworld.AwaitSession(app, id, func(s protocol.Session) bool {
			return protocol.Deref(s.TurnOwed) && s.State == protocol.SessionStateWaitingInput
		})
		if len(current.PullRequests) != 0 {
			t.Fatalf("%s restart restored a forgotten PR: %+v", restart.name, current.PullRequests)
		}
		t.Logf("%s restart: %s is current; forgotten PR stays absent", restart.name, id)
	}
}

func TestOutpostSwitchesKeepTheirHistoryWhileTheHomeAnswerWaits(t *testing.T) {
	for _, point := range []string{pausepoint.ConversationSwitchDecision, pausepoint.ConversationSwitchAnswer} {
		t.Run(point, func(t *testing.T) {
			home := testworld.NewStack(t)
			answer := home.PauseAt(point)
			outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			pair := testworld.PrepareOutpostPair(t, home, outpost)
			pair.Start()
			awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
			app := pair.App
			first := home.Spawn(app, fakeagent.Claude, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
			terminal := protocol.TerminalID(app.Terminal(first))
			agent := outpost.LaunchedCarrying(string(terminal))
			app.TypeLine(first, "first")
			agent.Prompted()
			agent.Reply("First ready. <!-- attn:state=waiting_input -->")
			testworld.AwaitSession(app, first, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) && s.Usage != nil })
			app.TypeLine(first, "/clear")
			agent.Prompted()
			answer.Await()
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: "middle\r"})
			agent.Prompted()
			agent.Reply("Middle ready. <!-- attn:state=waiting_input -->")
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: "/clear\r"})
			agent.Prompted()
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: "last\r"})
			agent.Prompted()
			agent.Reply("Last ready. <!-- attn:state=waiting_input -->")
			answer.Release()
			middle := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
				return e.Session != nil && protocol.Deref(e.Session.Succeeds) == protocol.SessionID(first)
			}).Session
			closedMiddle := testworld.Await(app, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
				return e.SessionLedgerEntry != nil && e.SessionLedgerEntry.ID == middle.ID
			}).SessionLedgerEntry
			if closedMiddle.Usage == nil || closedMiddle.Usage.TotalTokens != 2*len("Middle ready. <!-- attn:state=waiting_input -->") {
				t.Fatalf("middle cost: %+v", closedMiddle.Usage)
			}
			last := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
				return e.Session != nil && protocol.Deref(e.Session.Succeeds) == middle.ID
			}).Session
			current := testworld.AwaitSession(app, string(last.ID), func(s protocol.Session) bool {
				return protocol.Deref(s.TurnOwed) && s.Usage != nil && s.Usage.TotalTokens > 0
			})
			if app.Terminal(string(current.ID)) != string(terminal) {
				t.Fatal("the final conversation moved to another terminal")
			}
			t.Logf("%s: %s -> %s -> %s; final tokens=%d", point, first, middle.ID, last.ID, current.Usage.TotalTokens)
		})
	}
}

func TestAnOutpostConversationSwitchRebindsBeforeReload(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	app := pair.App
	id := home.Spawn(app, fakeagent.Claude, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	terminal := app.Terminal(id)
	agent := outpost.LaunchedCarrying(terminal)
	app.TypeLine(id, "before clear")
	agent.Prompted()
	agent.Reply("Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	app.TypeLine(id, "/clear")
	agent.Prompted()
	opened := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && protocol.Deref(e.Session.Succeeds) == protocol.SessionID(id)
	})
	next := *opened.Session
	app.TypeLine(string(next.ID), "after clear")
	agent.Prompted()
	conversation := agent.ConversationID
	agent.Reply("Switched. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, string(next.ID), func(s protocol.Session) bool {
		return protocol.Deref(s.TurnOwed) && s.Usage != nil && s.Usage.TotalTokens > 0
	})
	reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: next.ID, Cols: 100, Rows: 30}, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return r.ID == next.ID })
	if !reloaded.Success {
		t.Fatalf("reload: %s", protocol.Deref(reloaded.Error))
	}
	resumed := outpost.LaunchedCarrying(terminal)
	if resumed.ConversationID != conversation {
		t.Fatalf("reload resumed %s, want %s", resumed.ConversationID, conversation)
	}
	t.Logf("home bound %s -> %s on outpost; reload continues %s", id, next.ID, conversation)
}

func shareOutpostCodex(t *testing.T, pair *testworld.OutpostPair) {
	t.Helper()
	noCodex, err := exec.LookPath("false")
	if err != nil {
		t.Fatal(err)
	}
	if configured := pair.Home.Attn("settings", "set", "codex_executable", noCodex); configured.Code != 0 {
		t.Fatal(configured)
	}
	shareCodex(t, pair.App)
	outpostApp := pair.Outpost.App()
	if outpostApp.Initial.Settings["codex_shared_enabled"] != "true" {
		testworld.Await(outpostApp, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["codex_shared_enabled"] == "true" })
	}
	pair.Outpost.SetCodexFolderTrust(pair.Outpost.Path("agent"), "trusted")
}

func TestAHiddenOutpostCodexStaysLiveWhileOfflineAndCanBeShownAgain(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	reported := home.PauseEvery(pausepoint.OutpostRecoveryReported)
	applied := home.PauseEvery(pausepoint.OutpostRecoveryApplied)
	pair.Start()
	reported.Await()
	reported.Release()
	applied.Await()
	applied.Release()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	shareOutpostCodex(t, pair)
	app := pair.App
	id := home.Spawn(app, fakeagent.Codex, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	agent := outpost.LaunchedCarrying(app.Terminal(id))
	app.TypeLine(id, "before hiding")
	agent.Prompted()
	original := agent.ConversationID
	agent.Reply("Hidden soon. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	app.TypeLine(id, "/new")
	agent.Prompted()
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })
	if off := home.Attn("settings", "set", "outposts_enabled", "false"); off.Code != 0 {
		t.Fatal(off)
	}
	for _, restarted := range []bool{false, true} {
		if restarted {
			home.Stop()
			home.Start()
			app = home.App()
		}
		if err := home.Client().Unregister(protocol.SessionID(id)); err == nil || !strings.Contains(err.Error(), "outpost is unreachable") {
			t.Fatalf("hidden close after restart=%v: %v", restarted, err)
		}
		t.Logf("hidden live session %s refuses offline close (home restarted=%v)", id, restarted)
	}
	mark := len(app.Log())
	if on := home.Attn("settings", "set", "outposts_enabled", "true"); on.Code != 0 {
		t.Fatal(on)
	}
	outpostStateSince(app, "outpost", protocol.OutpostStateConnected, mark)
	reported.Await()
	spawned := testworld.Request(app, protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: "show-hidden", Agent: "codex", Cwd: outpost.Path("agent"), Host: protocol.Ptr(pair.Host), ProfileID: app.SelectedProfile(), ResumeSessionID: protocol.Ptr(original), Placement: &protocol.SessionPlacement{}, Cols: 100, Rows: 30}, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return string(r.ID) == id || r.ID == "show-hidden" })
	if !spawned.Success || string(spawned.ID) != id {
		t.Fatalf("show hidden: %+v, error: %s", spawned, protocol.Deref(spawned.Error))
	}
	reported.Release()
	applied.Await()
	applied.Release()
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return !protocol.Deref(s.Hidden) })
	shown := outpost.LaunchedCarrying(app.Terminal(id))
	mark = len(app.Log())
	app.TypeLine(id, "after showing")
	shown.Prompted()
	shown.Reply("Visible again. <!-- attn:state=waiting_input -->")
	current := testworld.AwaitSessionSince(app, mark, id, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWaitingInput && protocol.Deref(s.TurnOwed) && s.Usage != nil && s.Usage.TotalTokens > 0
	})
	t.Logf("shown again: %s reports tokens=%d", id, current.Usage.TotalTokens)
}

func TestAClosedOutpostConversationResumesWithItsOwnCostAfterAnOutpostRestart(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	app := pair.App
	id := home.Spawn(app, fakeagent.Claude, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	agent := outpost.LaunchedCarrying(app.Terminal(id))
	app.TypeLine(id, "first work")
	agent.Prompted()
	original := agent.ConversationID
	firstReply := "First work done. <!-- attn:state=waiting_input -->"
	agent.Reply(firstReply)
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return s.Usage != nil && s.Usage.TotalTokens == 2*len(firstReply) })
	app.TypeLine(id, "/clear")
	agent.Prompted()
	next := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && protocol.Deref(e.Session.Succeeds) == protocol.SessionID(id)
	}).Session
	closed := testworld.Await(app, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == id
	}).SessionLedgerEntry
	if closed.Usage == nil || closed.Usage.TotalTokens != 2*len(firstReply) {
		t.Fatalf("closed cost: %+v", closed.Usage)
	}
	mark := len(app.Log())
	outpost.Stop()
	outpost.Start()
	outpostStateSince(app, "outpost", protocol.OutpostStateConnected, mark)
	mark = len(app.Log())
	app.TypeLine(string(next.ID), "/resume "+original)
	agent.Prompted()
	testworld.AwaitSince(app, mark, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool { return e.Session != nil && string(e.Session.ID) == id })
	app.TypeLine(id, "second work")
	agent.Prompted()
	secondReply := "Second work done. <!-- attn:state=waiting_input -->"
	agent.Reply(secondReply)
	want := 2 * (len(firstReply) + len(secondReply))
	resumed := testworld.AwaitSession(app, id, func(s protocol.Session) bool { return s.Usage != nil && s.Usage.TotalTokens == want })
	t.Logf("closed %s at %d tokens; outpost restart and /resume continue at %d", id, closed.Usage.TotalTokens, resumed.Usage.TotalTokens)
}

func TestOutpostAgentLaunchNamesItsFirstSyncAndHarnessLimits(t *testing.T) {
	home := testworld.NewStack(t)
	syncing := home.PauseAt(pausepoint.NotebookStreamOpen)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	syncing.Await()
	withHost := func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) }
	early, _, _ := home.RequestSpawn(pair.App, fakeagent.Claude, outpost.Path("agent"), withHost)
	if early.Success || !strings.Contains(protocol.Deref(early.Error), "first sync") {
		t.Fatalf("first sync gate: success=%v error=%q", early.Success, protocol.Deref(early.Error))
	}
	t.Logf("first sync gate: %s", protocol.Deref(early.Error))
	for _, limit := range []struct {
		agent fakeagent.Harness
		chief bool
		want  string
	}{
		{fakeagent.Claude, true, "chief and crew run only at the home"},
		{fakeagent.Harness("pi"), false, "plugin harness pi runs only at the home"},
	} {
		denied, _, _ := home.RequestSpawn(pair.App, limit.agent, outpost.Path("agent"), withHost, func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(limit.chief) })
		if denied.Success || !strings.Contains(protocol.Deref(denied.Error), limit.want) {
			t.Fatalf("limit %s: %+v", limit.want, denied)
		}
	}
	syncing.Release()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	id := home.Spawn(pair.App, fakeagent.Claude, outpost.Path("agent"), withHost)
	outpost.LaunchedCarrying(pair.App.Terminal(id))
	pair.App.Send(protocol.SetChiefOfStaffMessage{Cmd: protocol.CmdSetChiefOfStaff, SessionID: protocol.SessionID(id), ChiefOfStaff: true})
	refused := testworld.Await(pair.App, protocol.EventChiefOfStaffResult, func(m protocol.ChiefOfStaffResultMessage) bool { return m.SessionID == protocol.SessionID(id) })
	if refused.Success || !strings.Contains(protocol.Deref(refused.Error), "chief runs only at the home") {
		t.Fatalf("chief toggle: %+v", refused)
	}
	t.Log("chief/crew and plugin launch limits named; chief toggle refused; first-sync completion permits Claude")
}

func TestAnOutpostReplaysItsPendingConversationAfterRestart(t *testing.T) {
	home := testworld.NewStack(t)
	deciding := home.PauseAt(pausepoint.ConversationSwitchDecision)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	app := pair.App
	first := home.Spawn(app, fakeagent.Claude, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	agent := outpost.LaunchedCarrying(app.Terminal(first))
	app.TypeLine(first, "before switching")
	agent.Prompted()
	agent.Reply("First. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, first, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	app.TypeLine(first, "/clear")
	agent.Prompted()
	deciding.Await()
	mark := len(app.Log())
	outpost.Stop()
	outpost.Start()
	deciding.Release()
	outpostStateSince(app, "outpost", protocol.OutpostStateConnected, mark)
	next := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && protocol.Deref(e.Session.Succeeds) == protocol.SessionID(first)
	}).Session
	app.TypeLine(string(next.ID), "after pending restart")
	agent.Prompted()
	reply := "Replayed. <!-- attn:state=waiting_input -->"
	agent.Reply(reply)
	current := testworld.AwaitSession(app, string(next.ID), func(s protocol.Session) bool {
		return protocol.Deref(s.TurnOwed) && s.Usage != nil && s.Usage.TotalTokens == 2*len(reply)
	})
	t.Logf("pending switch survived outpost restart: %s -> %s, tokens=%d", first, next.ID, current.Usage.TotalTokens)
}

func TestClosingAnOutpostConversationBeforeItsReplyDoesNotReopenIt(t *testing.T) {
	home := testworld.NewStack(t)
	answer := home.PauseAt(pausepoint.ConversationSwitchAnswer)
	applied := home.PauseEvery(pausepoint.OutpostRecoveryApplied)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	bound := outpost.PauseAt(pausepoint.ConversationCandidateBound)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	applied.Await()
	applied.Release()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	app := pair.App
	first := home.Spawn(app, fakeagent.Claude, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	agent := outpost.LaunchedCarrying(app.Terminal(first))
	app.TypeLine(first, "first")
	agent.Prompted()
	agent.Reply("First. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, first, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	app.TypeLine(first, "/clear")
	agent.Prompted()
	answer.Await()
	bound.Await()
	bound.Release()
	next := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && protocol.Deref(e.Session.Succeeds) == protocol.SessionID(first)
	}).Session
	if err := home.Client().Unregister(next.ID); err != nil {
		t.Fatal(err)
	}
	testworld.Await(app, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && e.SessionLedgerEntry.ID == next.ID
	})
	answer.Release()
	outpost.Stop()
	outpost.Start()
	applied.Await()
	applied.Release()
	entry, err := home.Client().SessionShow(next.ID)
	if err != nil || entry == nil || protocol.Deref(entry.Entry.ClosedAt) == "" {
		t.Fatalf("late reply reopened %s: %+v, %v", next.ID, entry, err)
	}
	listed, err := home.Client().List("")
	if err != nil || len(listed.Sessions) != 0 {
		t.Fatalf("closed conversations came back: %+v, %v", listed, err)
	}
	t.Logf("outbox bound %s before reply; close and outpost restart leave it closed", next.ID)
}

func TestClosingAnOutpostAgentDrainsItsFinalCostAndLeavesItsTranscriptReadable(t *testing.T) {
	for _, harness := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		t.Run(string(harness), func(t *testing.T) {
			home := testworld.NewStack(t)
			outpost := testworld.NewStack(t, testworld.WithAgents(harness))
			pair := testworld.PrepareOutpostPair(t, home, outpost)
			pair.Start()
			awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
			outpost.SetCodexFolderTrust(outpost.Path("agent"), "trusted")
			id := home.Spawn(pair.App, harness, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
			agent := outpost.LaunchedCarrying(pair.App.Terminal(id))
			pair.App.TypeLine(id, "final work")
			agent.Prompted()
			reply := "Final remote answer. <!-- attn:state=waiting_input -->"
			agent.ReplyUnheard(reply)
			if err := home.Client().Unregister(protocol.SessionID(id)); err != nil {
				t.Fatal(err)
			}
			closed := testworld.Await(pair.App, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
				return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == id
			}).SessionLedgerEntry
			if closed.Usage == nil || closed.Usage.TotalTokens != 2*len(reply) {
				t.Fatalf("final cost: %+v", closed.Usage)
			}
			page, err := home.Client().SessionTranscript(id, "")
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(page)
			if err != nil || !strings.Contains(string(encoded), "Final remote answer.") {
				t.Fatalf("closed remote transcript: %s, %v", encoded, err)
			}
			t.Logf("%s close drains %d tokens; closed transcript is readable from its machine", harness, closed.Usage.TotalTokens)
		})
	}
}

func TestOneOfTwoRemoteCodexTerminalsCanExitWhileItsConversationKeepsRunning(t *testing.T) {
	for _, scenario := range []string{"online", "offline-restart", "reload-during-recovery"} {
		t.Run(scenario, func(t *testing.T) {
			offline := scenario != "online"
			reload := scenario == "reload-during-recovery"
			home := testworld.NewStack(t)
			outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
			pair := testworld.PrepareOutpostPair(t, home, outpost)
			var exitHandled *testworld.Pause
			if offline {
				exitHandled = outpost.PauseAt(pausepoint.PtyExitHandled)
			}
			var reported, applied *testworld.Pause
			if reload {
				reported = home.PauseEvery(pausepoint.OutpostRecoveryReported)
				applied = home.PauseEvery(pausepoint.OutpostRecoveryApplied)
			}
			pair.Start()
			if reload {
				reported.Await()
				reported.Release()
				applied.Await()
				applied.Release()
			}
			awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
			shareOutpostCodex(t, pair)
			app := pair.App
			id := home.Spawn(app, fakeagent.Codex, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
			firstTerminal := app.Terminal(id)
			first := outpost.LaunchedCarrying(firstTerminal)
			app.TypeLine(id, "before adding a terminal")
			first.Prompted()
			first.Reply("Ready. <!-- attn:state=waiting_input -->")
			testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
			nextLaunch := outpost.NextLaunch()
			spawned := testworld.Request(app, protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: "second-terminal", Agent: "codex", Cwd: outpost.Path("agent"), Host: protocol.Ptr(pair.Host), ProfileID: app.SelectedProfile(), ResumeSessionID: protocol.Ptr(first.ConversationID), Placement: &protocol.SessionPlacement{}, Cols: 100, Rows: 30}, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return string(r.ID) == id || r.ID == "second-terminal" })
			if !spawned.Success || string(spawned.ID) != id {
				t.Fatalf("second terminal: %+v", spawned)
			}
			second := nextLaunch()
			if second.SessionID == firstTerminal {
				t.Fatal("second terminal reused the first")
			}
			if offline {
				requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"), "false")
			}
			mark := len(app.Log())
			second.Exit(0)
			if offline {
				exitHandled.Await()
				exitHandled.Release()
				outpost.Stop()
				outpost.Start()
				requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "true"), "true")
			}
			if reload {
				reported.Await()
				launchedAgain := outpost.NextLaunch()
				result := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(id), Cols: 100, Rows: 30}, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == id })
				if !result.Success {
					t.Fatalf("reload after exit: %s", protocol.Deref(result.Error))
				}
				reloaded := launchedAgain()
				if reloaded.SessionID != second.SessionID {
					t.Fatalf("reload terminal = %s, want %s", reloaded.SessionID, second.SessionID)
				}
				reported.Release()
				applied.Await()
				applied.Release()
				snapshot := home.App()
				bindings := snapshot.Initial.TerminalBindings
				snapshot.Close()
				kept := false
				for _, binding := range bindings {
					kept = kept || string(binding.TerminalID) == second.SessionID
				}
				if !kept {
					t.Fatalf("old recovery removed successfully reloaded terminal %s", second.SessionID)
				}
			} else {
				testworld.AwaitSince(app, mark, protocol.EventTerminalBindingsUpdated, func(e protocol.TerminalBindingsUpdatedMessage) bool {
					firstAlive := false
					for _, binding := range e.TerminalBindings {
						if string(binding.TerminalID) == second.SessionID {
							return false
						}
						firstAlive = firstAlive || string(binding.TerminalID) == firstTerminal
					}
					return firstAlive
				})
			}
			mark = len(app.Log())
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(firstTerminal), Data: "after the other terminal exits\r"})
			first.Prompted()
			first.Reply("Still running. <!-- attn:state=waiting_input -->")
			current := testworld.AwaitSessionSince(app, mark, id, func(s protocol.Session) bool {
				return protocol.Deref(s.TurnOwed) && s.State == protocol.SessionStateWaitingInput && s.Usage != nil
			})
			t.Logf("terminal %s exited; %s keeps conversation %s at %d tokens", second.SessionID, firstTerminal, first.ConversationID, current.Usage.TotalTokens)
			if err := home.Client().Unregister(protocol.SessionID(id)); err != nil {
				t.Fatal(err)
			}
			testworld.Await(app, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
				return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == id
			})
		})
	}
}

func TestEnrollmentLeavesHomeJobsDormantUntilTheMachineLeaves(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost.Vars = append(outpost.Vars, "ATTN_HEADLESS_TASKS=")
	outpost.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
		if strings.Contains(strings.Join(task.Argv, " "), "--json-schema") {
			task.Answer(`{"state":"waiting_input"}`)
		} else {
			task.Answer("Machine work title")
		}
	})
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	requireStdout(t, outpost.Attn("settings", "set", "headless_tasks.enabled", "false"), "false")
	app := outpost.App()
	id := outpost.Spawn(app, fakeagent.Claude, outpost.Path("abandoned"))
	outpost.Launched(id)
	cli := outpost.Client()
	planted, err := cli.SeedPlant(protocol.SessionID(id), "Interrupted local work", "Finish the interrupted work.", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SeedTransition(protocol.SessionID(id), planted.Seed.ID, "tend", "", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := cli.Unregister(protocol.SessionID(id)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(outpost.Path("abandoned")); err != nil {
		t.Fatal(err)
	}
	if review, err := cli.SeedReviewStart(); err != nil || review.Review == nil {
		t.Fatalf("start local review: %+v, %v", review, err)
	}
	awaitTask := func(kind string, predicate func(protocol.Task) bool) protocol.Task {
		t.Helper()
		for {
			mark := len(app.Log())
			tasks := testworld.Request(app, protocol.TaskListMessage{Cmd: protocol.CmdTaskList, RequestID: protocol.Ptr("local-tasks")}, protocol.EventTaskListResult, func(r protocol.TaskListResultMessage) bool { return r.RequestID == "local-tasks" }).Tasks
			for _, task := range tasks {
				if task.Kind == kind && predicate(task) {
					return task
				}
			}
			testworld.AwaitSince(app, mark, protocol.EventTasksChanged, func(protocol.WebSocketEvent) bool { return true })
		}
	}
	failed := awaitTask("garden_review_classify", func(task protocol.Task) bool { return task.State == "failed" })
	pair.Connect()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	mark := len(pair.App.Log())
	outpost.Stop()
	outpost.Start()
	outpostStateSince(pair.App, "outpost", protocol.OutpostStateConnected, mark)
	app = outpost.App()
	retried := testworld.Request(app, protocol.TaskRetryMessage{Cmd: protocol.CmdTaskRetry, TaskID: failed.ID, RequestID: protocol.Ptr("retry-local-review")}, protocol.EventTaskRetryResult, func(r protocol.TaskRetryResultMessage) bool { return r.RequestID == "retry-local-review" })
	if !retried.Success || retried.Task == nil || retried.Task.State != "queued" {
		t.Fatalf("retry while enrolled: %+v", retried)
	}
	requireStdout(t, home.Attn("settings", "set", "headless_tasks.enabled", "true"), "true")
	testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["headless_tasks.enabled"] == "true" })
	local := home.Spawn(pair.App, fakeagent.Claude, outpost.Path("machine-task"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	agent := outpost.LaunchedCarrying(pair.App.Terminal(local))
	pair.App.TypeLine(local, "Give this local machine task a title")
	agent.Prompted()
	agent.Reply("Title work. <!-- attn:state=waiting_input -->")
	awaitTask("session_title", func(task protocol.Task) bool { return task.Attempts > 0 && task.State != "running" })
	kept := awaitTask("garden_review_classify", func(task protocol.Task) bool { return task.ID == failed.ID })
	if kept.State != "queued" || kept.Attempts != 0 {
		t.Fatalf("enrolled home job was consumed: %+v", kept)
	}
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"), "false")
	requireStdout(t, outpost.Attn("enrollment", "leave"), "no longer enrolled")
	ran := awaitTask("garden_review_classify", func(task protocol.Task) bool { return task.ID == failed.ID && task.Attempts > 0 })
	t.Logf("home job %s stayed queued at attempt 0 while a machine job ran; after leave: %s, attempt %d", ran.ID, ran.State, ran.Attempts)
}

func TestLeavingEnrollmentBeforeRecoveryStopsTheOutpostsSharedCodex(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	shareOutpostCodex(t, pair)
	id := home.Spawn(pair.App, fakeagent.Codex, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	agent := outpost.LaunchedCarrying(pair.App.Terminal(id))
	pair.App.TypeLine(id, "before leaving")
	agent.Prompted()
	agent.Reply("Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	server := outpost.CodexServer()
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"), "false")
	outpost.Stop()
	recovery := outpost.PauseAt(pausepoint.DaemonStartupRecovery)
	outpost.StartHeldAt(recovery)
	requireStdout(t, outpost.Attn("enrollment", "leave"), "no longer enrolled")
	recovery.Release()
	server.AwaitStopped()
	t.Logf("outpost-only shared server stopped after enrollment leave before PTY recovery (conversation %s)", agent.ConversationID)
}
