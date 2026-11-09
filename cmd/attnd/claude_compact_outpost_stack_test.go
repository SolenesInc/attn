package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/fakeclaudeapi"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	outpostCompactReason = "the checkout fix landed"
	outpostCompactAsked  = "wrap up the checkout fix"
	outpostCompactRing   = "attn agent inbox"
	outpostCompactReply  = "Fixed and closing. <!-- attn:state=idle -->"
)

type realOutpostClaude struct {
	home, outpost *testworld.Stack
	pair          *testworld.OutpostPair
	model         *fakeclaudeapi.Model
	onOutpost     *testworld.Peer
	ptyExit       *testworld.Pause
	configDir     string
	session       string
	terminal      string
}

func newRealOutpostClaude(t *testing.T, willClose bool, vars ...string) *realOutpostClaude {
	t.Helper()
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not on PATH")
	}
	model := fakeclaudeapi.Start(t)
	var configDir, work string
	var ptyExit *testworld.Pause
	home, outpost, pair := newOutpostModPair(t, func(_, outpost *testworld.Stack) {
		if willClose {
			ptyExit = outpost.PauseAt(pausepoint.PtyExitHandled)
		}
		work = outpost.Path("shop")
		configDir = filepath.Join(outpost.Dir, "toolhome", ".claude")
		writeClaudeConfig(t, claude, configDir, work)
		if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{"permissions":{"allow":["Bash(attn agent inbox)","Bash(attn agent close:*)","Bash(attn seed standby:*)"]}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		outpost.Vars = append(outpost.Vars,
			"ATTN_CLAUDE_EXECUTABLE="+claude,
			"CLAUDE_CONFIG_DIR="+configDir,
			"ANTHROPIC_BASE_URL="+model.URL,
			"ANTHROPIC_API_KEY="+fakeclaudeapi.APIKey,
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
			"DISABLE_AUTOUPDATER=1",
		)
		outpost.Vars = append(outpost.Vars, vars...)
	}, testworld.OnPTYBackend(testworld.WorkerPTY))
	c := &realOutpostClaude{home: home, outpost: outpost, pair: pair, model: model, onOutpost: outpost.App(), ptyExit: ptyExit, configDir: configDir}
	c.session = home.Spawn(pair.App, fakeagent.Claude, work, func(msg *protocol.SpawnSessionMessage) { msg.Host = &pair.Host })
	c.terminal = pair.App.Terminal(c.session)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("worker screen:\n%s", home.Attn("agent", "peek", c.session).Stdout)
		}
	})
	outpost.AwaitTimer(daemon.ClaudeModPollHoldTimer)
	return c
}

func (c *realOutpostClaude) mail(t *testing.T, to, text string) {
	t.Helper()
	if sent := c.home.Run(testworld.Invocation{Args: []string{"agent", "msg", to, text}, Session: outpostModSender}); sent.Code != 0 {
		t.Fatalf("attn agent msg exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
	}
}

func (c *realOutpostClaude) closesWith(t *testing.T, app *testworld.Peer, reason string) {
	t.Helper()
	closed := testworld.Await(app, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == c.session
	})
	if got := protocol.Deref(closed.SessionLedgerEntry.CloseReason); got != reason {
		t.Errorf("the session closed with reason %q, want %q", got, reason)
	}
}

func (c *realOutpostClaude) compactedAfter(t *testing.T, resultOf string) {
	t.Helper()
	c.ptyExit.Await()
	c.ptyExit.Release()
	terminal := protocol.TerminalID(c.terminal)
	gone := testworld.Request(c.onOutpost, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == terminal })
	if gone.Success && protocol.Deref(gone.Running) {
		t.Fatalf("Claude's PTY still runs on the outpost after its close: %+v", gone)
	}
	entries := claudeTranscript(t, c.configDir)
	asked := slices.IndexFunc(entries, func(e claudeEntry) bool { return e.Type == "user" && strings.Contains(e.Text, resultOf) })
	if asked < 0 {
		t.Fatalf("Claude's transcript has no result with %q; entries: %+v", resultOf, entries)
	}
	boundary := slices.IndexFunc(entries, func(e claudeEntry) bool { return e.Type == "system" && e.Subtype == "compact_boundary" })
	if boundary < 0 || !entries[boundary].Timestamp.After(entries[asked].Timestamp) {
		t.Fatalf("Claude's transcript has no compact boundary after the close was asked at %s; entries: %+v", entries[asked].Timestamp, entries)
	}
}

func TestARealClaudeOnAnOutpostThatClosesCompactingCompactsThroughTheModAndThenCloses(t *testing.T) {
	t.Parallel()
	c := newRealOutpostClaude(t, true)
	c.model.RunBashAfter(outpostCompactRing, outpostCompactRing)
	c.model.RunBashAfter(outpostCompactAsked, `attn agent close --self --compact -m "`+outpostCompactReason+`"`)

	c.mail(t, c.session, outpostCompactAsked)

	c.closesWith(t, c.pair.App, outpostCompactReason)
	c.compactedAfter(t, "once its conversation is compacted")
}

func TestARealClaudeOnAnOutpostClosesAfterTheHomeLinkReturnsWhenItWasDownForTheCompactsOutcome(t *testing.T) {
	t.Parallel()
	c := newRealOutpostClaude(t, true)
	release := c.model.HoldCompacts()
	defer release()
	c.model.RunBashAfter(outpostCompactRing, outpostCompactRing)
	c.model.RunBashAfter(outpostCompactAsked, `attn agent close --self --compact -m "`+outpostCompactReason+`"`)
	c.mail(t, c.session, outpostCompactAsked)
	c.model.AwaitPrompt(t, fakeclaudeapi.CompactPrompt)

	c.pair.Disconnect()
	outpostReachSince(c.pair.App, 0, "outpost", protocol.OutpostReachUnreachable)
	mark := len(c.pair.App.Log())
	release()
	c.outpost.AwaitTimer(daemon.ClaudeModReportRetryTimer)

	c.pair.Reconnect()
	defer c.home.FireTimers(bridge.RetryTimer)()
	outpostReachSince(c.pair.App, mark, "outpost", protocol.OutpostReachReachable)
	defer c.outpost.FireTimers(daemon.ClaudeModReportRetryTimer)()
	c.outpost.FirePending(daemon.ClaudeModReportRetryTimer)
	c.closesWith(t, c.pair.App, outpostCompactReason)
	c.compactedAfter(t, "once its conversation is compacted")
}

func TestARealClaudeOnAnOutpostOnStandbyCompactsClosesAndResumesOnSeedMailWithItsConversation(t *testing.T) {
	t.Parallel()
	c := newRealOutpostClaude(t, true)
	app := c.pair.App
	var seed protocol.Seed
	c.outpost.Run(testworld.Invocation{Args: []string{"seed", "plant", "Merge the discount field", "--json"}, Terminal: c.terminal}).JSON(t, &seed)
	if tend := c.outpost.Run(testworld.Invocation{Args: []string{"seed", "tend", seed.ID}, Terminal: c.terminal}); tend.Code != 0 {
		t.Fatalf("seed tend exited %d: %s", tend.Code, tend.Stderr)
	}
	const note = "On approval: merge the pull request."
	c.model.RunBashAfter(outpostCompactRing, outpostCompactRing)
	c.model.RunBashAfter(outpostCompactAsked, `attn seed standby `+seed.ID+` -m "`+note+`" --close --compact`)

	c.mail(t, c.session, outpostCompactAsked)
	c.closesWith(t, app, "On standby for "+seed.ID+": "+note)
	c.compactedAfter(t, seed.ID)
	if shown := showSeed(t, c.home, outpostModSender, seed.ID); shown.Seed.Standby == nil {
		t.Fatalf("%s left standby when its tender closed", seed.ID)
	}

	mark, appMark := c.model.Mark(), len(app.Log())
	c.model.RunBashAfter(note, outpostCompactRing)
	c.mail(t, seed.ID, "the pull request is approved")
	if opening := c.model.AwaitPromptSince(t, mark, note); !strings.Contains(opening, fakeclaudeapi.CompactSummary) {
		t.Fatalf("the woken conversation's first request opens with %q, want Claude's compact summary", opening)
	}
	testworld.AwaitSince(app, appMark, protocol.EventGardenSeedsChanged, func(changed protocol.GardenSeedsChangedMessage) bool {
		return slices.ContainsFunc(changed.Seeds, func(s protocol.Seed) bool { return s.ID == seed.ID && s.Standby == nil })
	})
}

func TestARealClaudeDraftOnAnOutpostDoesNotStopACompactingClose(t *testing.T) {
	t.Parallel()
	c := newRealOutpostClaude(t, false)
	app := c.pair.App
	c.model.RunBashAfter(outpostCompactRing, outpostCompactRing)
	c.model.RunBashAfter(outpostCompactAsked, `attn agent close --self --compact -m "`+outpostCompactReason+`"`)

	app.Type(c.session, "please do not keep this draft", "keyboard")
	c.mail(t, c.session, outpostCompactAsked)
	closed := testworld.Await(app, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == c.session
	})
	if got := protocol.Deref(closed.SessionLedgerEntry.CloseReason); got != outpostCompactReason {
		t.Errorf("the session closed with reason %q, want %q", got, outpostCompactReason)
	}
}

func (m *outpostMod) closeCompacting(t *testing.T) {
	t.Helper()
	asked := m.outpost.Run(testworld.Invocation{Args: []string{"agent", "close", "--self", "--compact", "-m", compactOutpostReason}, Terminal: string(m.terminal)})
	if asked.Code != 0 {
		t.Fatalf("agent close --self --compact on the outpost exited %d: %s%s", asked.Code, asked.Stdout, asked.Stderr)
	}
	if report := m.run.ModNextCompactReport(); report.Outcome != fakeagent.ModCompactBusy {
		t.Fatalf("the mod reported the compact asked during the turn as %+v, want busy", report)
	}
}

func (m *outpostMod) closeReason(app *testworld.Peer) string {
	closed := testworld.Await(app, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == m.session
	})
	return protocol.Deref(closed.SessionLedgerEntry.CloseReason)
}

const compactOutpostReason = "done with the essay"

func TestACompactThatFailsOnAnOutpostStillClosesTheSessionSayingSo(t *testing.T) {
	m := newOutpostMod(t)
	m.workingOn(t, m.app, "write the essay")
	m.run.ModFailNextCompact("context window unavailable")
	m.closeCompacting(t)
	m.run.Reply(outpostCompactReply)
	reason := m.closeReason(m.app)
	if !strings.HasPrefix(reason, compactOutpostReason) || !strings.Contains(reason, "the compact did not run") || !strings.Contains(reason, "context window unavailable") {
		t.Fatalf("the session closed for %q, want it to say the compact did not run and why", reason)
	}
}

func TestACompactOnAnOutpostThatMeetsARunningTurnLetsMailThroughAndRetriesAtTheNextIdle(t *testing.T) {
	m := newOutpostMod(t)
	m.workingOn(t, m.app, "write the essay")
	m.run.ModBusyNextCompact(fakeagent.ModNewerTurnBegan)
	m.closeCompacting(t)
	m.run.Reply(outpostCompactReply)
	for report := m.run.ModNextCompactReport(); report.Reason != fakeagent.ModNewerTurnBegan; report = m.run.ModNextCompactReport() {
		if report.Outcome != fakeagent.ModCompactBusy {
			t.Fatalf("the mod reported the compact as %+v, want it refused for a newer turn", report)
		}
	}

	m.sendMail(t, m.session, "the review is in")
	if got := m.run.Prompted(); !m.run.ThroughMod || !strings.Contains(got, outpostCompactRing) {
		t.Fatalf("the agent took %q (through the mod: %t), want the ring of the mail", got, m.run.ThroughMod)
	}
	m.run.Reply(outpostCompactReply)
	if reason := m.closeReason(m.app); reason != compactOutpostReason {
		t.Fatalf("the session closed for %q, want %q", reason, compactOutpostReason)
	}
}

func TestADraftTypedWhileAnOutpostsFinishedCompactWaitsForTheHomeDoesNotStopTheClose(t *testing.T) {
	m := newOutpostMod(t)
	m.workingOn(t, m.app, "write the essay")
	arrived, release := m.run.ModHoldNextCompact()
	m.closeCompacting(t)
	m.run.Reply(outpostCompactReply)
	arrived()

	m.pair.Disconnect()
	outpostReachSince(m.app, 0, "outpost", protocol.OutpostReachUnreachable)
	mark := len(m.app.Log())
	release()
	m.outpost.AwaitTimer(daemon.ClaudeModReportRetryTimer)

	m.pair.Reconnect()
	defer m.home.FireTimers(bridge.RetryTimer)()
	outpostReachSince(m.app, mark, "outpost", protocol.OutpostReachReachable)
	const draft = "please keep this draft"
	m.app.Type(m.session, draft, "keyboard")
	m.app.AwaitScreen(m.session, draft)

	m.outpost.FirePending(daemon.ClaudeModReportRetryTimer)
	if reason := m.closeReason(m.app); reason != compactOutpostReason {
		t.Fatalf("the session closed for %q, want %q", reason, compactOutpostReason)
	}
}

func TestAnOutpostSessionWhoseSeedLeftStandbyStaysOpenInsteadOfCompacting(t *testing.T) {
	m := newOutpostMod(t)
	m.workingOn(t, m.app, "write the essay")
	var seed protocol.Seed
	m.outpost.Run(testworld.Invocation{Args: []string{"seed", "plant", "Merge the discount field", "--json"}, Terminal: string(m.terminal)}).JSON(t, &seed)
	for _, args := range [][]string{
		{"seed", "tend", seed.ID},
		{"seed", "standby", seed.ID, "-m", "On approval: merge.", "--close", "--compact"},
		{"seed", "standby", seed.ID, "--clear"},
	} {
		if done := m.outpost.Run(testworld.Invocation{Args: args, Terminal: string(m.terminal)}); done.Code != 0 {
			t.Fatalf("%v on the outpost exited %d: %s", args, done.Code, done.Stderr)
		}
	}
	m.run.Reply(outpostCompactReply)
	testworld.AwaitSession(m.app, m.session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	m.sendMail(t, m.session, "the review is in")
	if got := m.run.Prompted(); !m.run.ThroughMod || !strings.Contains(got, outpostCompactRing) {
		t.Fatalf("the agent took %q (through the mod: %t), want the ring of the mail", got, m.run.ThroughMod)
	}
	if got := m.run.ModCompactions(); got != 0 {
		t.Errorf("the mod compacted the conversation %d times, want never", got)
	}
}

func TestACompactWhoseTurnEndsWhileTheHomeLinkIsDownWaitsForTheHomeAndThenCloses(t *testing.T) {
	m := newOutpostMod(t)
	m.workingOn(t, m.app, "write the essay")
	m.closeCompacting(t)

	m.pair.Disconnect()
	outpostReachSince(m.app, 0, "outpost", protocol.OutpostReachUnreachable)
	mark := len(m.app.Log())
	m.run.Reply(outpostCompactReply)
	m.outpost.AwaitTimer(daemon.ClaudeModReportRetryTimer)
	if got := m.run.ModCompactions(); got != 0 {
		t.Fatalf("the mod compacted the conversation %d times before the home could check the close", got)
	}

	m.pair.Reconnect()
	defer m.home.FireTimers(bridge.RetryTimer)()
	outpostReachSince(m.app, mark, "outpost", protocol.OutpostReachReachable)
	defer m.outpost.FireTimers(daemon.ClaudeModReportRetryTimer)()
	m.outpost.FirePending(daemon.ClaudeModReportRetryTimer)
	if reason := m.closeReason(m.app); reason != compactOutpostReason {
		t.Fatalf("the session closed for %q, want %q", reason, compactOutpostReason)
	}
}

func TestAnOutpostSessionClosesAfterItsCompactWhenTheHomeRestartsDuringTheCompact(t *testing.T) {
	m := newOutpostMod(t)
	m.workingOn(t, m.app, "write the essay")
	arrived, release := m.run.ModHoldNextCompact()
	m.closeCompacting(t)
	m.run.Reply(outpostCompactReply)
	arrived()

	m.home.Stop()
	release()
	m.outpost.AwaitTimer(daemon.ClaudeModReportRetryTimer)
	m.home.Start()
	app := m.home.App()
	outpostStateOnConnect(app, "outpost", protocol.OutpostStateConnected)
	defer m.outpost.FireTimers(daemon.ClaudeModReportRetryTimer)()
	m.outpost.FirePending(daemon.ClaudeModReportRetryTimer)
	if reason := m.closeReason(app); reason != compactOutpostReason {
		t.Fatalf("the session closed for %q, want %q", reason, compactOutpostReason)
	}
}

func TestAnOutpostSessionStaysOpenAndTakesMailWhenTheOutpostRestartsDuringItsCompact(t *testing.T) {
	m := newOutpostMod(t)
	m.workingOn(t, m.app, "write the essay")
	arrived, release := m.run.ModHoldNextCompact()
	m.closeCompacting(t)
	m.run.Reply(outpostCompactReply)
	arrived()

	m.outpost.Stop()
	m.outpost.Start()
	outpostStateOnConnect(m.app, "outpost", protocol.OutpostStateConnected)
	m.outpost.AwaitTimer(daemon.ClaudeModPollHoldTimer)
	release()
	report := m.run.ModNextCompactReport()
	for report.Outcome == fakeagent.ModCompactBusy {
		report = m.run.ModNextCompactReport()
	}
	if report.Outcome != fakeagent.ModCompactCompacted {
		t.Fatalf("the mod reported the compact as %+v, want compacted", report)
	}

	m.sendMail(t, m.session, "the review is in")
	if got := m.run.Prompted(); !m.run.ThroughMod || !strings.Contains(got, outpostCompactRing) {
		t.Fatalf("the agent took %q (through the mod: %t), want the ring of the mail", got, m.run.ThroughMod)
	}
}

func TestAnOutpostSessionClosesWhenItsCompactNeverReportsAnOutcome(t *testing.T) {
	m := newOutpostMod(t)
	m.workingOn(t, m.app, "write the essay")
	arrived, _ := m.run.ModHoldNextCompact()
	m.closeCompacting(t)
	m.run.Reply(outpostCompactReply)
	arrived()

	defer m.outpost.FireTimers(daemon.CompactOutcomeTripwireTimer)()
	reason := m.closeReason(m.app)
	if !strings.HasPrefix(reason, compactOutpostReason) || !strings.Contains(reason, "compact_outcome_tripwire=") {
		t.Fatalf("the session closed for %q, want the tripwire named", reason)
	}
	m.run.AwaitGone()
}

func TestASharedCodexOnAnOutpostThatClosesCompactingCompactsAndThenCloses(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	shareOutpostCodex(t, pair)
	app := pair.App
	id := home.Spawn(app, fakeagent.Codex, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	terminal := app.Terminal(id)
	agent := outpost.LaunchedCarrying(terminal)
	app.TypeLine(id, "write the essay")
	agent.Prompted()
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	asked := outpost.Run(testworld.Invocation{Args: []string{"agent", "close", "--self", "--compact", "-m", compactOutpostReason}, Terminal: terminal})
	if asked.Code != 0 {
		t.Fatalf("agent close --self --compact on the outpost exited %d: %s%s", asked.Code, asked.Stdout, asked.Stderr)
	}
	agent.Reply(outpostCompactReply)
	closed := testworld.Await(app, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == id
	})
	if got := protocol.Deref(closed.SessionLedgerEntry.CloseReason); got != compactOutpostReason {
		t.Fatalf("the session closed for %q, want %q", got, compactOutpostReason)
	}
}
