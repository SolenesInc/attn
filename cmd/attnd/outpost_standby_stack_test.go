package main_test

import (
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const outpostStandbyNote = "On approval: merge the pull request."

func (m *outpostMod) onStandby(t *testing.T, flags ...string) string {
	t.Helper()
	var seed protocol.Seed
	m.outpost.Run(testworld.Invocation{Args: []string{"seed", "plant", "Merge the discount field", "--json"}, Terminal: string(m.terminal)}).JSON(t, &seed)
	standby := append([]string{"seed", "standby", seed.ID, "-m", outpostStandbyNote}, flags...)
	for _, args := range [][]string{{"seed", "tend", seed.ID}, standby} {
		if done := m.outpost.Run(testworld.Invocation{Args: args, Terminal: string(m.terminal)}); done.Code != 0 {
			t.Fatalf("%v on the outpost exited %d: %s", args, done.Code, done.Stderr)
		}
	}
	return seed.ID
}

func (m *outpostMod) onStandbyAndClosed(t *testing.T) string {
	t.Helper()
	m.workingOn(t, m.app, "write the essay")
	m.run.Reply("Done. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(m.app, m.session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	seed := m.onStandby(t)
	m.awaitClosed()
	return seed
}

func (m *outpostMod) awaitClosed() {
	testworld.Await(m.app, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == m.session
	})
}

func (m *outpostMod) resumeBlocked(t *testing.T, seed string) string {
	t.Helper()
	standby := showSeed(t, m.home, outpostModSender, seed).Seed.Standby
	if standby == nil {
		t.Fatalf("%s left standby", seed)
	}
	return protocol.Deref(standby.ResumeBlocked)
}

func TestAnOutpostLostDuringTheResumesDirectoryCheckResumesTheTenderWhenItReturns(t *testing.T) {
	var inspecting *testworld.Pause
	m := newOutpostModPrepared(t, func(home, _ *testworld.Stack) { inspecting = home.PauseAt(pausepoint.TenderDirectoryInspecting) })
	seed := m.onStandbyAndClosed(t)

	sent := make(chan testworld.Result, 1)
	go func() {
		sent <- m.home.Run(testworld.Invocation{Args: []string{"agent", "msg", seed, "the pull request is approved"}, Session: outpostModSender})
	}()
	inspecting.Await()
	m.pair.Disconnect()
	outpostReachSince(m.app, 0, "outpost", protocol.OutpostReachUnreachable)
	inspecting.Release()
	if result := <-sent; result.Code != 0 || !strings.Contains(result.Stdout+result.Stderr, "unreachable") {
		t.Fatalf("mail sent while the outpost was lost: %+v, want it queued naming the outpost", result)
	}
	if blocked := m.resumeBlocked(t, seed); blocked != "" {
		t.Fatalf("a lost link blocked the wake: %s", blocked)
	}

	next := m.outpost.NextLaunch()
	m.home.AwaitTimer(bridge.RetryTimer)
	m.pair.Reconnect()
	m.home.Advance(bridge.ReconnectInterval)
	woken := next()
	if got := woken.Prompted(); !woken.ThroughMod || !strings.Contains(got, outpostStandbyNote) {
		t.Fatalf("the woken Claude took %q (through the mod: %t), want the standby note through the mod", got, woken.ThroughMod)
	}
}

func TestAnOutpostDirectoryThatIsGoneOrUnusableBlocksTheResume(t *testing.T) {
	for name, c := range map[string]struct {
		leave func(t *testing.T, path string)
		want  string
	}{
		"missing": {func(*testing.T, string) {}, "no longer exists"},
		"a file": {func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "cannot be opened"},
	} {
		t.Run(name, func(t *testing.T) {
			m := newOutpostMod(t)
			seed := m.onStandbyAndClosed(t)
			path := m.outpost.Path("shop")
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			c.leave(t, path)

			m.sendMail(t, seed, "the pull request is approved")
			if blocked := m.resumeBlocked(t, seed); !strings.Contains(blocked, c.want) {
				t.Fatalf("the wake is blocked for %q, want %q", blocked, c.want)
			}
		})
	}
}

func (m *outpostMod) answerReturnedBeforeItsPlacementAnswers(t *testing.T, answering *testworld.Pause) string {
	t.Helper()
	seed := m.onStandby(t, "--stay")
	m.workingOn(t, m.app, "write the essay")
	asked, err := m.home.Client().AskOpen(protocol.SessionID(m.session), seed, "Deploys empty the cache.\n\n:::question\nWhich store should keep the cache?\n- [ ] Postgres\n- [ ] Redis\n:::\n", "", m.home.Path())
	if err != nil {
		t.Fatalf("ask about %s: %v", seed, err)
	}
	m.app.Send(protocol.AskAnswerMessage{Cmd: protocol.CmdAskAnswer, RequestID: "answer", AskID: asked.ID, Answers: []protocol.AskAnswer{{Key: "q1", Picked: []string{"Postgres"}}}})
	answering.Await()
	m.modEvent(t, fakeagent.ModEventHeld, "Answer: Postgres")
	m.esc(t, "the answer")
	answering.Release()
	m.modEvent(t, fakeagent.ModEventRefusedStop, "Answer: Postgres")
	if blocked := m.resumeBlocked(t, seed); blocked != "" {
		t.Fatalf("the returned answer blocked the wake: %s", blocked)
	}
	return seed
}

func TestAnAnswerReturnedBeforeItsPlacementAnswersReachesClaudeOnTheNextTurn(t *testing.T) {
	var answering *testworld.Pause
	m := newOutpostModPrepared(t, func(_, outpost *testworld.Stack) { answering = outpost.PauseAt(pausepoint.RingPlacementAnswering) })
	seed := m.answerReturnedBeforeItsPlacementAnswers(t, answering)

	mark := len(m.app.Log())
	m.workingOn(t, m.app, "carry on")
	m.modEvent(t, fakeagent.ModEventHeld, "Answer: Postgres")
	if appended := m.run.ModToolBoundary(); len(appended) != 1 || !strings.Contains(appended[0], "Answer: Postgres") {
		t.Fatalf("the tool boundary appended %v, want the answer", appended)
	}
	testworld.AwaitSince(m.app, mark, protocol.EventGardenSeedsChanged, func(changed protocol.GardenSeedsChangedMessage) bool {
		for _, s := range changed.Seeds {
			if s.ID == seed && s.Standby == nil {
				return true
			}
		}
		return false
	})
}

func TestAnAnswerReturnedBeforeItsPlacementAnswersResumesATenderThatCloses(t *testing.T) {
	var answering *testworld.Pause
	m := newOutpostModPrepared(t, func(_, outpost *testworld.Stack) { answering = outpost.PauseAt(pausepoint.RingPlacementAnswering) })
	m.answerReturnedBeforeItsPlacementAnswers(t, answering)

	next := m.outpost.NextLaunch()
	testworld.Request(m.app, protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: protocol.SessionID(m.session)}, protocol.EventSessionCloseResult,
		func(r protocol.SessionCloseResultMessage) bool { return string(r.SessionID) == m.session })
	woken := next()
	if got := woken.Prompted(); !woken.ThroughMod || !strings.Contains(got, "Answer: Postgres") {
		t.Fatalf("the woken Claude took %q (through the mod: %t), want the answer through the mod", got, woken.ThroughMod)
	}
}
