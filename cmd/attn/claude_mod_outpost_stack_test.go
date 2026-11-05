package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/claudemod"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const outpostModSender = "rev-1111-2222"

type outpostMod struct {
	home, outpost *testworld.Stack
	pair          *testworld.OutpostPair
	session       string
	terminal      protocol.TerminalID
	run           *fakeagent.Run
	app           *testworld.Peer
}

func (m *outpostMod) esc(t *testing.T, want string) {
	t.Helper()
	mark := len(m.app.Log())
	m.pressEsc(t, want)
	m.awaitStopped(m.app, mark)
}

func (m *outpostMod) pressEsc(t *testing.T, want string) {
	t.Helper()
	if returned := m.run.ModEsc(); len(returned) != 1 {
		t.Fatalf("Esc returned %v, want %s", returned, want)
	}
}

func (m *outpostMod) awaitStopped(app *testworld.Peer, mark int) {
	testworld.AwaitSessionSince(app, mark, m.session, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateIdle && protocol.Deref(s.StateReason) == "turn_aborted"
	})
}

func newOutpostMod(t *testing.T) *outpostMod {
	t.Helper()
	return newOutpostModPrepared(t, func(_, _ *testworld.Stack) {})
}

func newOutpostModPrepared(t *testing.T, prepare func(home, outpost *testworld.Stack)) *outpostMod {
	t.Helper()
	home, outpost := testworld.NewStack(t), testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	prepare(home, outpost)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	requireStdout(t, home.Attn("settings", "set", "claude_mod_enabled", "true"), "claude_mod_enabled = true")
	requireStdout(t, home.Attn("settings", "set", "asks_enabled", "true"), "asks_enabled = true")
	copied := outpost.App()
	pair.Connect()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	testworld.Await(copied, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["claude_mod_enabled"] == "true" })
	register(t, home, outpostModSender, "reviewer")
	m := &outpostMod{home: home, outpost: outpost, pair: pair, app: pair.App}
	m.session = home.Spawn(pair.App, fakeagent.Claude, outpost.Path("shop"), func(msg *protocol.SpawnSessionMessage) {
		msg.Host = &pair.Host
		msg.Label = protocol.Ptr("deploy work")
	})
	m.terminal = protocol.TerminalID(pair.App.Terminal(m.session))
	m.run = outpost.LaunchedCarrying(string(m.terminal))
	m.run.SessionID = m.session
	outpost.AwaitTimer(daemon.ClaudeModPollHoldTimer)
	return m
}

func (m *outpostMod) sendMail(t *testing.T, to, text string) {
	t.Helper()
	sent := m.home.Run(testworld.Invocation{Args: []string{"agent", "msg", to, text}, Session: outpostModSender})
	if sent.Code != 0 {
		t.Fatalf("attn agent msg exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
	}
}

func (m *outpostMod) workingOn(t *testing.T, app *testworld.Peer, prompt string) {
	t.Helper()
	m.run.Input(prompt)
	if got := m.run.Prompted(); got != prompt {
		t.Fatalf("the agent took %q, want %q", got, prompt)
	}
	testworld.AwaitSession(app, m.session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
}

func (m *outpostMod) ringHeld(t *testing.T) {
	t.Helper()
	if err := m.outpost.Client().UpdateState(m.terminal, protocol.StateWaitingInput); err != nil {
		t.Fatal(err)
	}
	m.modEvent(t, fakeagent.ModEventHeld, "attn agent inbox")
}

func (m *outpostMod) modEvent(t *testing.T, kind, text string) {
	t.Helper()
	for {
		event := m.run.ModNext()
		if event.Kind == kind && strings.Contains(event.Text, text) {
			return
		}
		if event.Kind != fakeagent.ModEventRefusedStop {
			t.Fatalf("the mod reported %+v, want %s of %q", event, kind, text)
		}
	}
}

func TestAMessageFromHomeReachesAnOutpostClaudeThroughTheMod(t *testing.T) {
	m := newOutpostMod(t)
	m.sendMail(t, m.session, "the discount is applied after tax")
	got := m.run.Prompted()
	if !m.run.ThroughMod || !strings.Contains(got, "attn agent inbox") {
		t.Fatalf("the agent took %q (through the mod: %t), want the ring through the mod", got, m.run.ThroughMod)
	}
}

func TestARingReturnedOnAnOutpostIsUnreadAtHomeWithItsAttemptTakenBack(t *testing.T) {
	m := newOutpostMod(t)
	app := m.pair.App
	m.workingOn(t, app, "write the essay")
	m.sendMail(t, m.session, "the discount is applied after tax")
	for range 4 {
		m.ringHeld(t)
		m.esc(t, "the ring")
		m.workingOn(t, app, "carry on")
	}
	m.ringHeld(t)
	if appended := m.run.ModToolBoundary(); len(appended) != 1 || !strings.Contains(appended[0], "attn agent inbox") {
		t.Fatalf("the tool boundary appended %v, want the ring", appended)
	}
}

func TestARingReturnedWhileTheHomeLinkIsDownIsUnreadAtHomeAfterItComesBack(t *testing.T) {
	m := newOutpostMod(t)
	app := m.pair.App
	m.workingOn(t, app, "write the essay")
	m.sendMail(t, m.session, "the discount is applied after tax")
	m.ringHeld(t)

	m.pair.Disconnect()
	outpostReachSince(app, 0, "outpost", protocol.OutpostReachUnreachable)
	mark := len(app.Log())
	m.pressEsc(t, "the ring")
	m.pair.Reconnect()
	defer m.home.FireTimers(bridge.RetryTimer)()
	defer m.outpost.FireTimers(daemon.ClaudeModReportRetryTimer)()
	m.awaitStopped(app, mark)

	m.workingOn(t, app, "carry on")
	m.ringHeld(t)
}

func TestARingReturnedWhileTheHomeRestartsIsUnreadAtHomeAfterIt(t *testing.T) {
	m := newOutpostMod(t)
	m.workingOn(t, m.pair.App, "write the essay")
	m.sendMail(t, m.session, "the discount is applied after tax")
	m.ringHeld(t)

	m.home.Stop()
	m.pressEsc(t, "the ring")
	defer m.outpost.FireTimers(daemon.ClaudeModReportRetryTimer)()
	m.home.Start()
	app := m.home.App()
	outpostStateOnConnect(app, "outpost", protocol.OutpostStateConnected)

	m.workingOn(t, app, "carry on")
	m.ringHeld(t)
}

func TestARingTheModReturnsAfterTheOutpostRestartsIsUnreadAtHome(t *testing.T) {
	m := newOutpostMod(t)
	m.workingOn(t, m.pair.App, "write the essay")
	m.sendMail(t, m.session, "the discount is applied after tax")
	m.ringHeld(t)

	m.outpost.Stop()
	m.outpost.Start()
	outpostStateOnConnect(m.pair.App, "outpost", protocol.OutpostStateConnected)
	m.outpost.AwaitTimer(daemon.ClaudeModPollHoldTimer)
	m.esc(t, "the ring")

	m.workingOn(t, m.pair.App, "carry on")
	m.ringHeld(t)
}

func TestARingQueuedOnAnOutpostSurvivesItsRestartWhileTheHomeIsAway(t *testing.T) {
	m := newOutpostMod(t)
	app := m.pair.App
	m.workingOn(t, app, "write the essay")
	m.sendMail(t, m.session, "the discount is applied after tax")
	m.ringHeld(t)

	m.pair.Disconnect()
	outpostReachSince(app, 0, "outpost", protocol.OutpostReachUnreachable)
	m.pressEsc(t, "the ring")
	m.outpost.AwaitTimer(daemon.ClaudeModReportRetryTimer)
	m.outpost.Stop()
	defer m.outpost.FireTimers(daemon.ClaudeModReportRetryTimer)()
	m.outpost.Start()

	m.pair.Reconnect()
	defer m.home.FireTimers(bridge.RetryTimer)()
	m.workingOn(t, app, "carry on")
	m.ringHeld(t)
}

func TestAUserMessageTheModReturnsAfterTheOutpostRestartsReachesTheSessionOnce(t *testing.T) {
	m := newOutpostMod(t)
	app := m.pair.App
	m.workingOn(t, app, "write the essay")
	const words = "check the rounding"
	app.Send(protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "held", SessionID: protocol.SessionID(m.session), Text: words})
	m.modEvent(t, fakeagent.ModEventHeld, words)

	m.outpost.Stop()
	m.outpost.Start()
	outpostStateOnConnect(app, "outpost", protocol.OutpostStateConnected)
	m.outpost.AwaitTimer(daemon.ClaudeModPollHoldTimer)
	m.pressEsc(t, "the annotation")
	m.modEvent(t, fakeagent.ModEventRefusedStop, words)
	if got := m.run.Prompted(); got != words || m.run.ThroughMod {
		t.Fatalf("the agent took %q (through the mod: %t), want %q typed", got, m.run.ThroughMod, words)
	}
	const probe = "nothing came before this"
	app.TypeLine(m.session, probe)
	if got := m.run.Prompted(); got != probe {
		t.Fatalf("the agent took %q before %q, want nothing in between", got, probe)
	}
}

func TestARingTheModReturnsAfterTheHomeDaemonRestartsIsUnread(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	requireStdout(t, s.Attn("settings", "set", "claude_mod_enabled", "true"), "claude_mod_enabled = true")
	register(t, s, outpostModSender, "reviewer")
	app := s.App()
	session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	terminal := protocol.TerminalID(app.Terminal(session))
	run := s.LaunchedCarrying(string(terminal))
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)
	m := &outpostMod{home: s, outpost: s, session: session, terminal: terminal, run: run}
	m.workingOn(t, app, "write the essay")
	m.sendMail(t, session, "the discount is applied after tax")
	m.ringHeld(t)

	s.Stop()
	s.Start()
	app = s.App()
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)
	m.app = app
	m.esc(t, "the ring")
	m.workingOn(t, app, "carry on")
	m.ringHeld(t)
}

func TestAReportTheHomeRefusesDoesNotBlockTheOnesBehindIt(t *testing.T) {
	m := newOutpostMod(t)
	m.workingOn(t, m.pair.App, "write the essay")
	m.sendMail(t, m.session, "the discount is applied after tax")
	m.ringHeld(t)

	socket := claudemod.SocketPath(m.outpost.Dir)
	client := http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	refused, err := json.Marshal(map[string]any{"terminal": "a-terminal-the-daemon-does-not-know", "instance": "gone", "returns": []map[string]any{
		{"id": "inbox-ring/refused", "text": "refused", "ring": protocol.InboxRingRecord{Address: "not an address", ProfileID: "p", Stamp: "s"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Post("http://attn/claude/return", "application/json", bytes.NewReader(refused))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	m.esc(t, "the ring")
	m.workingOn(t, m.pair.App, "carry on")
	m.ringHeld(t)
}

func TestAUserMessageTheOutpostModReturnsWhileWorkingReachesTheAgentWhenItParks(t *testing.T) {
	var waits *testworld.Pause
	m := newOutpostModPrepared(t, func(_, outpost *testworld.Stack) {
		outpost.Vars = append(outpost.Vars, "ATTN_HEADLESS_TASKS=on")
		waits = outpost.PauseEvery(pausepoint.ClaudeModReturnWaits)
	})
	app := m.pair.App
	m.workingOn(t, app, "deploy to staging")
	const words = "check the rollout"
	app.Send(protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "held", SessionID: protocol.SessionID(m.session), Text: words})
	if taken := testworld.Await(app, protocol.EventSessionAnnotationsSubmitResult, func(r protocol.SessionAnnotationsSubmitResultMessage) bool {
		return r.RequestID == "held"
	}); taken.Status != "delivered" {
		t.Fatalf("the annotation was answered %+v, want it delivered", taken)
	}
	m.modEvent(t, fakeagent.ModEventHeld, words)

	m.run.ModReload()
	waits.Await()
	waits.Release()

	m.run.Stream("The deploy is running in the background.")
	transcript := fakeagent.WriteClaudeTranscript(t, m.outpost.Path("shop"), m.run.ConversationID)
	if err := m.outpost.Client().SendStop(m.terminal, transcript.Path, client.StopFacts{BackgroundTasks: []protocol.StopBackgroundTask{
		{Type: "background_session", Status: "running", Name: protocol.Ptr("./deploy.sh staging")},
	}}); err != nil {
		t.Fatalf("stop with background work: %v", err)
	}
	m.run.ProgramStatus("state=done:app=claude-code")
	task := m.outpost.HeadlessTask()
	if !strings.Contains(task.Prompt, "Classify how this assistant message ends the agent's turn") {
		t.Fatalf("the outpost asked %q, want the turn's verdict", task.Prompt)
	}
	task.Answer(`{"verdict":"PARKED"}`)

	if got := m.run.Prompted(); got != words {
		t.Fatalf("the parked agent took %q, want the returned annotation", got)
	}
}

func TestAUserMessageTheOutpostModReturnsReachesTheSessionOnce(t *testing.T) {
	m := newOutpostMod(t)
	app := m.pair.App
	m.workingOn(t, app, "write the essay")
	const words = "check the rounding"
	app.Send(protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "held", SessionID: protocol.SessionID(m.session), Text: words})
	if taken := testworld.Await(app, protocol.EventSessionAnnotationsSubmitResult, func(r protocol.SessionAnnotationsSubmitResultMessage) bool {
		return r.RequestID == "held"
	}); taken.Status != "delivered" {
		t.Fatalf("the annotation was answered %+v, want it delivered", taken)
	}
	m.modEvent(t, fakeagent.ModEventHeld, words)

	m.pressEsc(t, "the annotation")
	m.modEvent(t, fakeagent.ModEventRefusedStop, words)
	if got := m.run.Prompted(); got != words || m.run.ThroughMod {
		t.Fatalf("the agent took %q (through the mod: %t), want %q typed", got, m.run.ThroughMod, words)
	}
	const probe = "nothing came before this"
	app.TypeLine(m.session, probe)
	if got := m.run.Prompted(); got != probe {
		t.Fatalf("the agent took %q before %q, want nothing in between", got, probe)
	}
}

func TestSeedMailWakesAnOutpostClaudeTenderThroughTheMod(t *testing.T) {
	m := newOutpostMod(t)
	app := m.pair.App
	m.workingOn(t, app, "write the essay")
	m.run.Reply("Done. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, m.session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	var seed protocol.Seed
	m.outpost.Run(testworld.Invocation{Args: []string{"seed", "plant", "Merge the discount field", "--json"}, Terminal: string(m.terminal)}).JSON(t, &seed)
	if tend := m.outpost.Run(testworld.Invocation{Args: []string{"seed", "tend", seed.ID}, Terminal: string(m.terminal)}); tend.Code != 0 {
		t.Fatalf("seed tend exited %d: %s", tend.Code, tend.Stderr)
	}
	const note = "On approval: merge the pull request."
	if standby := m.outpost.Run(testworld.Invocation{Args: []string{"seed", "standby", seed.ID, "-m", note, "--close"}, Terminal: string(m.terminal)}); standby.Code != 0 {
		t.Fatalf("seed standby --close on the outpost exited %d: %s", standby.Code, standby.Stderr)
	}
	testworld.Await(app, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == m.session
	})

	next := m.outpost.NextLaunch()
	m.sendMail(t, seed.ID, "the pull request is approved")
	woken := next()
	got := woken.Prompted()
	if !woken.ThroughMod || !strings.Contains(got, note) {
		t.Fatalf("the woken Claude took %q (through the mod: %t), want the standby note through the mod", got, woken.ThroughMod)
	}
}

func TestAHeldAnswerKeepsAnOutpostTendersSeedOnStandbyUntilClaudeTakesIt(t *testing.T) {
	m := newOutpostMod(t)
	app := m.pair.App
	var seed protocol.Seed
	m.outpost.Run(testworld.Invocation{Args: []string{"seed", "plant", "Merge the discount field", "--json"}, Terminal: string(m.terminal)}).JSON(t, &seed)
	for _, args := range [][]string{{"seed", "tend", seed.ID}, {"seed", "standby", seed.ID, "-m", "On approval: merge the pull request."}} {
		if done := m.outpost.Run(testworld.Invocation{Args: args, Terminal: string(m.terminal)}); done.Code != 0 {
			t.Fatalf("%v on the outpost exited %d: %s", args, done.Code, done.Stderr)
		}
	}
	m.workingOn(t, app, "write the essay")

	const words = "Answer: Postgres"
	asked, err := m.home.Client().AskOpen(protocol.SessionID(m.session), seed.ID, "Deploys empty the cache.\n\n:::question\nWhich store should keep the cache?\n- [ ] Postgres\n- [ ] Redis\n:::\n", "", m.home.Path())
	if err != nil {
		t.Fatalf("ask about %s: %v", seed.ID, err)
	}
	answered := testworld.Request(app, protocol.AskAnswerMessage{
		Cmd: protocol.CmdAskAnswer, RequestID: "answer", AskID: asked.ID, Answers: []protocol.AskAnswer{{Key: "q1", Picked: []string{"Postgres"}}},
	}, protocol.EventAskAnswerResult, func(r protocol.AskAnswerResultMessage) bool { return r.RequestID == "answer" })
	if !answered.Success {
		t.Fatalf("answering failed: %s", protocol.Deref(answered.Error))
	}
	m.modEvent(t, fakeagent.ModEventHeld, words)
	if shown := showSeed(t, m.home, outpostModSender, seed.ID); shown.Seed.Standby == nil {
		t.Fatalf("%s left standby while the mod held its answer", seed.ID)
	}

	m.esc(t, "the answer")
	m.modEvent(t, fakeagent.ModEventRefusedStop, words)
	if shown := showSeed(t, m.home, outpostModSender, seed.ID); shown.Seed.Standby == nil {
		t.Fatalf("%s left standby after Esc returned its answer unread", seed.ID)
	}

	mark := len(app.Log())
	m.workingOn(t, app, "carry on")
	m.modEvent(t, fakeagent.ModEventHeld, words)
	if appended := m.run.ModToolBoundary(); len(appended) != 1 || !strings.Contains(appended[0], words) {
		t.Fatalf("the tool boundary appended %v, want the answer", appended)
	}
	testworld.AwaitSince(app, mark, protocol.EventGardenSeedsChanged, func(changed protocol.GardenSeedsChangedMessage) bool {
		for _, s := range changed.Seeds {
			if s.ID == seed.ID && s.Standby == nil {
				return true
			}
		}
		return false
	})
}

func TestAnOutpostClaudeCannotCloseWithCompactAndIsToldWhy(t *testing.T) {
	m := newOutpostMod(t)
	closed := m.outpost.Run(testworld.Invocation{Args: []string{"agent", "close", "--self", "--compact", "-m", "done with the work"}, Terminal: string(m.terminal)})
	if closed.Code == 0 || !strings.Contains(closed.Stderr+closed.Stdout, "on an outpost") {
		t.Fatalf("agent close --self --compact exited %d with %q%q, want a refusal that names the outpost", closed.Code, closed.Stdout, closed.Stderr)
	}
}

func TestWithTheSettingOffAnOutpostClaudeIsTypedInto(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	register(t, home, outpostModSender, "reviewer")
	session := home.Spawn(pair.App, fakeagent.Claude, outpost.Path("shop"), func(msg *protocol.SpawnSessionMessage) { msg.Host = &pair.Host })
	run := outpost.LaunchedCarrying(pair.App.Terminal(session))
	sent := home.Run(testworld.Invocation{Args: []string{"agent", "msg", session, "the discount is applied after tax"}, Session: outpostModSender})
	if sent.Code != 0 {
		t.Fatalf("attn agent msg exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
	}
	if got := run.Prompted(); run.ThroughMod || !strings.Contains(got, "attn agent inbox") {
		t.Fatalf("the agent took %q (through the mod: %t), want the ring typed", got, run.ThroughMod)
	}
}
