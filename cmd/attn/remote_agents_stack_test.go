package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

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
	announcements := []*testworld.Peer{app}
	assertOneLaunch := func() {
		t.Helper()
		count := 0
		for _, peer := range announcements {
			for _, e := range peer.Received() {
				if e.Event == protocol.EventSessionRegistered && e.Session != nil && string(e.Session.ID) == id {
					count++
				}
			}
		}
		if count != 1 {
			t.Fatalf("launch announcements = %d, want exactly one", count)
		}
	}
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
	mark := app.EventCount()
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"), "false")
	outpostReachSince(app, mark, "outpost", protocol.OutpostReachUnreachable)
	if err := home.Client().SnoozeSession("", protocol.SessionID(id), time.Now().Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "outpost is unreachable") {
		t.Fatalf("offline snooze = %v", err)
	}
	message, err := home.Client().AgentMsg(id, protocol.SessionID(sender), "continue when connected")
	if err != nil || message == nil || !strings.Contains(message.Detail, "outpost, which is unreachable") {
		t.Fatalf("offline message = %+v, %v", message, err)
	}
	t.Logf("offline message: %s", message.Detail)
	mark = app.EventCount()
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "true"), "true")
	outpostReachSince(app, mark, "outpost", protocol.OutpostReachReachable)
	if prompt := outpost.CodexServer().Prompted(agent.ConversationID); !strings.Contains(prompt, "attn") {
		t.Fatalf("queued inbox ring = %q", prompt)
	}
	outpost.CodexServer().Reply(agent.ConversationID, "Connected. <!-- attn:state=waiting_input -->")
	testworld.AwaitSessionSince(app, mark, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	assertOneLaunch()
	for _, restart := range []struct {
		name  string
		stack *testworld.Stack
	}{{"home", home}, {"outpost", outpost}} {
		mark = app.EventCount()
		restart.stack.Stop()
		if restart.name == "outpost" {
			outpostReachSince(app, mark, "outpost", protocol.OutpostReachUnreachable)
			mark = app.EventCount()
		}
		restart.stack.Start()
		if restart.name == "home" {
			app = home.App()
			announcements = append(announcements, app)
			outpostStateOnConnect(app, "outpost", protocol.OutpostStateConnected)
		} else {
			outpostReachSince(app, mark, "outpost", protocol.OutpostReachReachable)
		}
		app.TypeLine(id, "after "+restart.name+" restart")
		agent.Prompted()
		agent.Reply("Still current. <!-- attn:state=waiting_input -->")
		current := testworld.AwaitSession(app, id, func(s protocol.Session) bool {
			return protocol.Deref(s.TurnOwed) && s.State == protocol.SessionStateWaitingInput
		})
		if len(current.PullRequests) != 0 {
			t.Fatalf("%s restart restored a forgotten PR: %+v", restart.name, current.PullRequests)
		}
		assertOneLaunch()
		t.Logf("%s restart: %s is current; forgotten PR stays absent; exactly one launch announcement", restart.name, id)
	}
}

func TestOutpostSwitchesKeepTheirHistoryWhileTheHomeOutcomeWaits(t *testing.T) {
	for _, point := range []string{pausepoint.ConversationSwitchDecision, pausepoint.ConversationSwitchOutcome} {
		t.Run(point, func(t *testing.T) {
			home := testworld.NewStack(t)
			outcome := home.PauseAt(point)
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
			outcome.Await()
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: "middle\r"})
			agent.Prompted()
			agent.Reply("Middle ready. <!-- attn:state=waiting_input -->")
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: "/clear\r"})
			agent.Prompted()
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: "last\r"})
			agent.Prompted()
			agent.Reply("Last ready. <!-- attn:state=waiting_input -->")
			outcome.Release()
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
	outpostReachSince(app, mark, "outpost", protocol.OutpostReachReachable)
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
	outpostReachSince(app, mark, "outpost", protocol.OutpostReachUnreachable)
	mark = app.EventCount()
	outpost.Start()
	outpostReachSince(app, mark, "outpost", protocol.OutpostReachReachable)
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
	outpostReachSince(app, mark, "outpost", protocol.OutpostReachUnreachable)
	mark = app.EventCount()
	outpost.Start()
	deciding.Release()
	outpostReachSince(app, mark, "outpost", protocol.OutpostReachReachable)
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
	outcome := home.PauseAt(pausepoint.ConversationSwitchOutcome)
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
	outcome.Await()
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
	outcome.Release()
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
	for _, scenario := range []struct {
		name                 string
		harness              fakeagent.Harness
		beforeTranscriptCopy bool
	}{
		{name: "claude", harness: fakeagent.Claude},
		{name: "codex", harness: fakeagent.Codex},
		{name: "claude-before-transcript-copy", harness: fakeagent.Claude, beforeTranscriptCopy: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			harness := scenario.harness
			home := testworld.NewStack(t)
			outpost := testworld.NewStack(t, testworld.WithAgents(harness))
			pair := testworld.PrepareOutpostPair(t, home, outpost)
			var copied *testworld.Pause
			if scenario.beforeTranscriptCopy {
				copied = outpost.PauseAt(pausepoint.CopyBatchApplied)
			}
			pair.Start()
			if copied != nil {
				requireStdout(t, home.Attn("settings", "set", "queue_quiet_notifications", "true"), "queue_quiet_notifications = true")
				copied.Await()
				defer copied.Release()
			}
			awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
			outpost.SetCodexFolderTrust(outpost.Path("agent"), "trusted")
			id := home.Spawn(pair.App, harness, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
			agent := outpost.LaunchedCarrying(pair.App.Terminal(id))
			if scenario.beforeTranscriptCopy {
				for {
					window := testworld.Request(pair.App, protocol.SessionMessagesGetMessage{
						Cmd: protocol.CmdSessionMessagesGet, RequestID: "before-first-prompt", SessionID: protocol.SessionID(id),
					}, protocol.EventSessionMessagesGetResult, func(r protocol.SessionMessagesGetResultMessage) bool { return r.RequestID == "before-first-prompt" })
					if window.Status != protocol.SessionMessageWindowStatusDiscovering {
						if window.Status != protocol.SessionMessageWindowStatusUnavailable {
							t.Fatalf("message window before first prompt: %+v", window)
						}
						break
					}
					testworld.Await(pair.App, protocol.EventSessionMessagesChanged, func(e protocol.SessionMessagesChangedMessage) bool { return string(e.SessionID) == id })
				}
			}
			pair.App.TypeLine(id, "final work")
			agent.Prompted()
			reply := "Final remote answer. <!-- attn:state=waiting_input -->"
			if scenario.beforeTranscriptCopy {
				agent.Stream(reply)
			} else {
				agent.ReplyUnheard(reply)
			}
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
	outpostReachSince(pair.App, mark, "outpost", protocol.OutpostReachUnreachable)
	mark = pair.App.EventCount()
	outpost.Start()
	outpostReachSince(pair.App, mark, "outpost", protocol.OutpostReachReachable)
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
	testworld.AwaitSession(pair.App, local, func(s protocol.Session) bool { return s.Label == "Machine work title" })
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

func TestOutpostConversationHistorySurvivesSwitchesWhileDisconnected(t *testing.T) {
	for _, scenario := range []string{"clear", "resume-closed", "two-clears", "outpost-restart"} {
		t.Run(scenario, func(t *testing.T) {
			home := testworld.NewStack(t)
			outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			pair := testworld.PrepareOutpostPair(t, home, outpost)
			pair.Start()
			awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
			app := pair.App
			first := home.Spawn(app, fakeagent.Claude, outpost.Path("offline-switch"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
			terminal := protocol.TerminalID(app.Terminal(first))
			agent := outpost.LaunchedCarrying(string(terminal))
			app.TypeLine(first, "original work")
			agent.Prompted()
			original := agent.ConversationID
			firstReply := "Original ready. <!-- attn:state=waiting_input -->"
			agent.Reply(firstReply)
			testworld.AwaitSession(app, first, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) && s.Usage != nil })
			current := first
			command := "/clear"
			if scenario == "resume-closed" {
				app.TypeLine(first, "/clear")
				agent.Prompted()
				next := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
					return e.Session != nil && protocol.Deref(e.Session.Succeeds) == protocol.SessionID(first)
				}).Session
				current = string(next.ID)
				app.TypeLine(current, "second conversation")
				agent.Prompted()
				agent.Reply("Second ready. <!-- attn:state=waiting_input -->")
				testworld.AwaitSession(app, current, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) && s.Usage != nil })
				command = "/resume " + original
			}
			restarting := len(app.Log())
			outpost.Stop()
			outpostStateSince(app, "outpost", protocol.OutpostStateLost, restarting)
			observed := outpost.PauseAt(pausepoint.ConversationSwitchObserved)
			started := len(app.Log())
			outpost.Start()
			outpostStateSince(app, "outpost", protocol.OutpostStateConnected, started)
			input := command + "\roffline work\r"
			mark := len(app.Log())
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: input})
			observed.Await()
			requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"))
			outpostStateSince(app, "outpost", protocol.OutpostStateLost, mark)
			pendingAt := time.Now()
			observed.Release()
			agent.Prompted()
			if prompt := agent.Prompted(); prompt != "offline work" {
				t.Fatalf("offline prompt = %q", prompt)
			}
			middleReply := "Offline ready. <!-- attn:state=waiting_input -->"
			agent.Reply(middleReply)
			lastReply := middleReply
			if scenario == "two-clears" {
				agent.Input("/clear")
				agent.Prompted()
				agent.Input("last offline work")
				if prompt := agent.Prompted(); prompt != "last offline work" {
					t.Fatalf("last prompt = %q", prompt)
				}
				lastReply = "Last offline ready. <!-- attn:state=waiting_input -->"
				agent.Reply(lastReply)
			}
			if scenario == "outpost-restart" {
				outpost.Stop()
				outpost.Start()
			}
			reconnectedAt := time.Now()
			connectionMark := app.EventCount()
			requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "true"))
			outpostReachSince(app, connectionMark, "outpost", protocol.OutpostReachReachable)
			var next *protocol.Session
			if scenario == "resume-closed" {
				next = testworld.AwaitSince(app, mark, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool { return e.Session != nil && string(e.Session.ID) == first }).Session
			} else {
				next = testworld.AwaitSince(app, mark, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
					return e.Session != nil && protocol.Deref(e.Session.Succeeds) == protocol.SessionID(current)
				}).Session
			}
			if scenario == "two-clears" {
				closed := testworld.AwaitSince(app, mark, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
					return e.SessionLedgerEntry != nil && e.SessionLedgerEntry.ID == next.ID
				}).SessionLedgerEntry
				if closed.Usage == nil || closed.Usage.TotalTokens != 2*len(middleReply) {
					t.Fatalf("offline intermediate usage = %+v", closed.Usage)
				}
				next = testworld.AwaitSince(app, mark, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
					return e.Session != nil && protocol.Deref(e.Session.Succeeds) == next.ID
				}).Session
			}
			want := 2 * len(lastReply)
			if scenario == "resume-closed" {
				want += 2 * len(firstReply)
			}
			ready := testworld.AwaitSessionSince(app, mark, string(next.ID), func(s protocol.Session) bool {
				return protocol.Deref(s.TurnOwed) && s.Usage != nil && s.Usage.TotalTokens == want
			})
			closed := testworld.AwaitSince(app, mark, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
				return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == current
			}).SessionLedgerEntry
			predecessorTokens := 2 * len(firstReply)
			if scenario == "resume-closed" {
				predecessorTokens = 2 * len("Second ready. <!-- attn:state=waiting_input -->")
			}
			if protocol.Deref(closed.ClosedAt) == "" || closed.Usage == nil || closed.Usage.TotalTokens != predecessorTokens {
				t.Fatalf("offline predecessor close: %+v, want tokens=%d", closed, predecessorTokens)
			}
			if app.Terminal(string(ready.ID)) != string(terminal) || protocol.Deref(ready.Host) != pair.Host {
				t.Fatalf("offline successor changed placement: %+v", ready)
			}
			if scenario != "outpost-restart" {
				opened, err := time.Parse(time.RFC3339Nano, protocol.Deref(ready.TurnOpenedAt))
				if err != nil || opened.Before(pendingAt) || !opened.Before(reconnectedAt) {
					t.Fatalf("turn opened %s, want between switch %s and reconnect %s: %v", protocol.Deref(ready.TurnOpenedAt), pendingAt, reconnectedAt, err)
				}
			}
			t.Logf("%s: offline conversation returns in terminal %s, host %s, tokens=%d, turn=%s", scenario, terminal, pair.Host, ready.Usage.TotalTokens, protocol.Deref(ready.TurnOpenedAt))
		})
	}
}

func TestAResumedOutpostConversationSurvivesRecoveryBeforeItsCopyArrives(t *testing.T) {
	home := testworld.NewStack(t)
	started := home.PauseEvery(pausepoint.OutpostRecoveryStarted)
	applied := home.PauseEvery(pausepoint.OutpostRecoveryApplied)
	decision := home.PauseEvery(pausepoint.ConversationSwitchDecision)
	outcome := home.PauseEvery(pausepoint.ConversationSwitchOutcome)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	started.Await()
	started.Release()
	applied.Await()
	applied.Release()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	app := pair.App
	first := home.Spawn(app, fakeagent.Claude, outpost.Path("resume-before-copy"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	terminal := app.Terminal(first)
	agent := outpost.LaunchedCarrying(terminal)
	app.TypeLine(first, "original work")
	agent.Prompted()
	original := agent.ConversationID
	reply := "Original ready. <!-- attn:state=waiting_input -->"
	agent.Reply(reply)
	testworld.AwaitSession(app, first, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) && s.Usage != nil })
	app.TypeLine(first, "/clear")
	agent.Prompted()
	decision.Await()
	decision.Release()
	outcome.Await()
	outcome.Release()
	second := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && protocol.Deref(e.Session.Succeeds) == protocol.SessionID(first)
	}).Session
	app.TypeLine(string(second.ID), "second work")
	agent.Prompted()
	agent.Reply("Second ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, string(second.ID), func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) && s.Usage != nil })
	mark := len(app.Log())
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"), "false")
	outpostStateSince(app, "outpost", protocol.OutpostStateLost, mark)
	agent.Input("/resume " + original)
	agent.Prompted()
	agent.Input("offline resumed work")
	agent.Prompted()
	agent.Reply(reply)
	mark = len(app.Log())
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "true"), "true")
	started.Await()
	decision.Await()
	decision.Release()
	outcome.Await()
	testworld.AwaitSince(app, mark, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool { return e.Session != nil && string(e.Session.ID) == first })
	started.Release()
	applied.Await()
	applied.Release()
	outcome.Release()
	ready := testworld.AwaitSessionSince(app, mark, first, func(s protocol.Session) bool {
		return protocol.Deref(s.TurnOwed) && s.Usage != nil && s.Usage.TotalTokens == 4*len(reply)
	})
	if app.Terminal(first) != terminal || protocol.Deref(ready.Host) != pair.Host {
		t.Fatalf("resumed placement: %+v", ready)
	}
	t.Logf("recovery kept resumed session %s in live terminal %s before its copy arrived; tokens=%d", first, terminal, ready.Usage.TotalTokens)
}

func TestAnOutpostConversationStaysRecoverableWhenItsWorkerIsGone(t *testing.T) {
	for _, harness := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		t.Run(string(harness), func(t *testing.T) {
			home := testworld.NewStack(t)
			outpost := testworld.NewStack(t, testworld.WithAgents(harness))
			pair := testworld.PrepareOutpostPair(t, home, outpost)
			pair.Start()
			awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
			app := pair.App
			id := home.Spawn(app, harness, outpost.Path("missing-worker"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
			terminal := app.Terminal(id)
			agent := outpost.LaunchedCarrying(terminal)
			app.TypeLine(id, "before losing the worker")
			agent.Prompted()
			conversation := agent.ConversationID
			first := "Ready before loss. <!-- attn:state=waiting_input -->"
			agent.Reply(first)
			testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) && s.Usage != nil })
			mark := len(app.Log())
			outpost.Reboot()
			outpostStateSince(app, "outpost", protocol.OutpostStateLost, mark)
			connectionMark := app.EventCount()
			outpost.Start()
			outpostReachSince(app, connectionMark, "outpost", protocol.OutpostReachReachable)
			testworld.AwaitSessionSince(app, mark, id, func(s protocol.Session) bool { return s.State == protocol.SessionStateRecoverable })
			reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(id), Cols: 100, Rows: 30}, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == id })
			if !reloaded.Success {
				t.Fatalf("recover remote conversation: %s", protocol.Deref(reloaded.Error))
			}
			resumed := outpost.LaunchedCarrying(terminal)
			if resumed.ConversationID != conversation {
				t.Fatalf("recovered conversation %s, want %s", resumed.ConversationID, conversation)
			}
			mark = len(app.Log())
			app.TypeLine(id, "after recovering")
			resumed.Prompted()
			second := "Recovered ready. <!-- attn:state=waiting_input -->"
			resumed.Reply(second)
			ready := testworld.AwaitSessionSince(app, mark, id, func(s protocol.Session) bool {
				return protocol.Deref(s.TurnOwed) && s.Usage != nil && s.Usage.TotalTokens == 2*(len(first)+len(second))
			})
			t.Logf("%s remote-only conversation %s remains recoverable with its worker gone; reload keeps %d tokens", harness, conversation, ready.Usage.TotalTokens)
		})
	}
}

func TestADroppedOutpostSwitchKeepsItsInFlightDeliveryUsable(t *testing.T) {
	home := testworld.NewStack(t)
	owners := home.PauseEvery(pausepoint.ConversationOwnerResolved)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	dropped := outpost.PauseAt(pausepoint.ConversationSwitchDropped)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	shareOutpostCodex(t, pair)
	app := pair.App
	spawnWaiting := func(prompt string) (string, *fakeagent.Run) {
		t.Helper()
		id := home.Spawn(app, fakeagent.Codex, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
		agent := outpost.LaunchedCarrying(app.Terminal(id))
		app.TypeLine(id, prompt)
		agent.Prompted()
		agent.Reply("Ready. <!-- attn:state=waiting_input -->")
		testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
		return id, agent
	}
	first, one := spawnWaiting("checkout")
	original := one.ConversationID
	app.TypeLine(first, "/new")
	one.Prompted()
	dropFirst := owners.Take()
	conversation := one.ConversationID
	server := outpost.CodexServer()
	halt := sync.OnceFunc(func() { server.Halt(conversation) })
	t.Cleanup(halt)
	awaitDelivery, releaseDelivery := server.HoldNextTurnReply()
	delivery := home.App()
	request := protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: uuid.NewString(), SessionID: protocol.SessionID(first), Text: "fix checkout"}
	delivery.Send(request)
	awaitDelivery()
	second := home.Spawn(app, fakeagent.Codex, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) {
		m.Host = protocol.Ptr(pair.Host)
		m.ResumeSessionID = protocol.Ptr(conversation)
	})
	outpost.LaunchedCarrying(app.Terminal(second))
	dropFirst()
	dropped.Await()
	releaseDelivery()
	dropped.Release()
	placed := testworld.Await(delivery, protocol.EventSessionAnnotationsSubmitResult, func(m protocol.SessionAnnotationsSubmitResultMessage) bool { return m.RequestID == request.RequestID })
	if placed.Status != "delivered" || server.Prompted(conversation) != request.Text {
		t.Fatalf("in-flight delivery: %+v", placed)
	}
	for _, id := range []string{first, second} {
		request.RequestID, request.SessionID, request.Text = uuid.NewString(), protocol.SessionID(id), "continue "+id
		placed = testworld.Request(delivery, request, protocol.EventSessionAnnotationsSubmitResult, func(m protocol.SessionAnnotationsSubmitResultMessage) bool { return m.RequestID == request.RequestID })
		if placed.Status != "delivered" || server.Prompted(conversation) != request.Text {
			t.Fatalf("delivery after dropped switch to %s: %+v", id, placed)
		}
	}
	halt()
	for _, row := range []struct{ id, native string }{{first, original}, {second, conversation}} {
		terminal := app.Terminal(row.id)
		reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(row.id), Cols: 100, Rows: 30}, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == row.id })
		if !reloaded.Success {
			t.Fatal(reloaded)
		}
		if resumed := outpost.LaunchedCarrying(terminal); resumed.ConversationID != row.native {
			t.Fatalf("reload %s resumed %s, want %s", row.id, resumed.ConversationID, row.native)
		}
	}
	t.Log("dropped candidate keeps in-flight and subsequent delivery; reload retains each recorded conversation")
}

func TestARemoteReloadHealsItsConversationOnTheNextPrompt(t *testing.T) {
	totals := make(map[string]int)
	for _, reload := range []bool{false, true} {
		name := "control"
		if reload {
			name = "delayed-switch-after-reload"
		}
		t.Run(name, func(t *testing.T) {
			home := testworld.NewStack(t)
			var deciding *testworld.Pause
			if reload {
				deciding = home.PauseAt(pausepoint.ConversationSwitchDecision)
			}
			outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			pair := testworld.PrepareOutpostPair(t, home, outpost)
			pair.Start()
			awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
			app := pair.App
			first := home.Spawn(app, fakeagent.Claude, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
			terminal := app.Terminal(first)
			agent := outpost.LaunchedCarrying(terminal)
			firstReply := "Ready. <!-- attn:state=waiting_input -->"
			app.TypeLine(first, "before reload")
			agent.Prompted()
			conversation := agent.ConversationID
			agent.Reply(firstReply)
			testworld.AwaitSession(app, first, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) && s.Usage != nil })
			if reload {
				app.TypeLine(first, "/clear")
				agent.Prompted()
				deciding.Await()
				reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(first), Cols: 100, Rows: 30}, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == first })
				if !reloaded.Success {
					t.Fatal(reloaded)
				}
				agent = outpost.LaunchedCarrying(terminal)
				if agent.ConversationID != conversation {
					t.Fatalf("reload resumed %s, want %s", agent.ConversationID, conversation)
				}
			}
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: "after reload\r"})
			agent.Prompted()
			secondReply := "Replacement ready. <!-- attn:state=waiting_input -->"
			var staleID protocol.SessionID
			if reload {
				deciding.Release()
				stale := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
					return e.Session != nil && protocol.Deref(e.Session.Succeeds) == protocol.SessionID(first)
				}).Session
				staleID = stale.ID
				t.Logf("stale decision committed %s while terminal %s runs %s", stale.ID, terminal, conversation)
				testworld.AwaitSession(app, string(staleID), func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			}
			finishedAt := time.Now()
			finishedMark := len(app.Log())
			agent.Reply(secondReply)
			if reload {
				finished := testworld.AwaitSessionSince(app, finishedMark, string(staleID), func(s protocol.Session) bool {
					return protocol.Deref(s.TurnOwed) && s.State != protocol.SessionStateWorking && !protocol.Timestamp(protocol.Deref(s.TurnOpenedAt)).Time().Before(finishedAt)
				})
				t.Logf("autonomous finish observed on %s: state=%s turn=%v seen=%s", staleID, finished.State, protocol.Deref(finished.TurnOwed), finished.LastSeen)
			}
			mark := len(app.Log())
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: "after stale commit\r"})
			agent.Prompted()
			thirdReply := "Healed ready. <!-- attn:state=waiting_input -->"
			agent.Reply(thirdReply)
			want := 2 * len(firstReply+secondReply+thirdReply)
			ready := testworld.AwaitSessionSince(app, mark, first, func(s protocol.Session) bool {
				return protocol.Deref(s.TurnOwed) && s.State == protocol.SessionStateWaitingInput && s.Usage != nil && s.Usage.TotalTokens == want
			})
			if app.Terminal(first) != terminal {
				t.Fatal("next prompt moved the original conversation to another terminal")
			}
			mark = app.EventCount()
			outpost.Stop()
			outpostReachSince(app, mark, "outpost", protocol.OutpostReachUnreachable)
			mark = app.EventCount()
			outpost.Start()
			outpostReachSince(app, mark, "outpost", protocol.OutpostReachReachable)
			mark = len(app.Log())
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: "after restart\r"})
			agent.Prompted()
			if agent.ConversationID != conversation {
				t.Fatalf("restart changed conversation to %s", agent.ConversationID)
			}
			lastReply := "Still original. <!-- attn:state=waiting_input -->"
			agent.Reply(lastReply)
			final := testworld.AwaitSessionSince(app, mark, first, func(s protocol.Session) bool {
				return protocol.Deref(s.TurnOwed) && s.State == protocol.SessionStateWaitingInput && s.Usage != nil && s.Usage.TotalTokens == ready.Usage.TotalTokens+2*len(lastReply)
			})
			if app.Terminal(first) != terminal {
				t.Fatal("restart moved the original conversation to another terminal")
			}
			totals[name] = final.Usage.TotalTokens
			t.Logf("%s: next-prompt tokens=%d final tokens=%d native=%s terminal=%s", name, ready.Usage.TotalTokens, final.Usage.TotalTokens, conversation, terminal)
		})
	}
	if len(totals) == 2 && totals["control"] != totals["delayed-switch-after-reload"] {
		t.Fatalf("reload tokens=%d, control=%d", totals["delayed-switch-after-reload"], totals["control"])
	}
}

func TestOutpostClaudeConversationSharingRespectsTheMachine(t *testing.T) {
	for _, crossMachine := range []bool{false, true} {
		name := "same-machine"
		if crossMachine {
			name = "different-machines"
		}
		t.Run(name, func(t *testing.T) {
			home := testworld.NewStack(t)
			outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			pair := testworld.PrepareOutpostPair(t, home, outpost)
			other := outpost
			if crossMachine {
				other = testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
				pair.SSH.Use(other.World)
				other.Start()
			}
			pair.Start()
			awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
			app := pair.App
			otherHost := pair.Host
			if crossMachine {
				address := bridgeAddress(t)
				pair.SSH.Route(address, other)
				app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "other", Destination: address})
				otherHost = outpostState(app, "other", protocol.OutpostStateConnected).ID
				otherPair := &testworld.OutpostPair{Home: home, Outpost: other, SSH: pair.SSH, App: app, Host: otherHost, Destination: address}
				awaitNotebookState(t, otherPair, protocol.OutpostNotebookStateInSync)
			}
			spawn := func(machine *testworld.Stack, host string) (string, *fakeagent.Run) {
				t.Helper()
				id := home.Spawn(app, fakeagent.Claude, machine.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(host) })
				agent := machine.LaunchedCarrying(app.Terminal(id))
				app.TypeLine(id, "prepare conversation")
				agent.Prompted()
				agent.Reply("Ready. <!-- attn:state=waiting_input -->")
				testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
				return id, agent
			}
			first, one := spawn(outpost, pair.Host)
			second, two := spawn(other, otherHost)
			if crossMachine {
				transcript := func(machine *testworld.Stack, native string) string {
					t.Helper()
					files, err := filepath.Glob(filepath.Join(machine.Dir, "toolhome", ".claude", "projects", "*", native+".jsonl"))
					if err != nil || len(files) != 1 {
						t.Fatalf("provider transcript %s: %v %v", native, files, err)
					}
					return files[0]
				}
				data, err := os.ReadFile(transcript(outpost, one.ConversationID))
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(filepath.Dir(transcript(other, two.ConversationID)), one.ConversationID+".jsonl"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			mark := len(app.Log())
			app.TypeLine(second, "/resume "+one.ConversationID)
			two.Prompted()
			if crossMachine {
				opened := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
					return e.Session != nil && protocol.Deref(e.Session.Succeeds) == protocol.SessionID(second)
				}).Session
				second = string(opened.ID)
			}
			for _, row := range []struct{ id, peer, host string }{{first, second, pair.Host}, {second, first, otherHost}} {
				x := testworld.AwaitSessionSince(app, mark, row.id, func(s protocol.Session) bool {
					return slices.Equal(s.ConversationSharedWith, []protocol.SessionID{protocol.SessionID(row.peer)})
				})
				if protocol.Deref(x.Host) != row.host {
					t.Fatalf("sharing moved %s to host %s", row.id, protocol.Deref(x.Host))
				}
			}
			mark = len(app.Log())
			one.Exit(1)
			testworld.AwaitSessionSince(app, mark, second, func(s protocol.Session) bool { return len(s.ConversationSharedWith) == 0 })
			app.TypeLine(second, "continue after the other terminal exits")
			two.Prompted()
			two.Reply("Survives. <!-- attn:state=waiting_input -->")
			testworld.AwaitSessionSince(app, mark, second, func(s protocol.Session) bool {
				return protocol.Deref(s.TurnOwed) && s.State == protocol.SessionStateWaitingInput
			})
			t.Logf("%s: distinct owners %s/%s retain hosts %s/%s; shared warning clears on exit", name, first, second, pair.Host, otherHost)
		})
	}
}
