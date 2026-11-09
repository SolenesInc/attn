package daemon_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/testworld"
)

const standbyNote = "On approval: merge the pull request."

func standbyAs(t *testing.T, cli *client.Client, delegated *protocol.DelegateResult, close bool) (*protocol.SeedStandbyResult, error) {
	t.Helper()
	return cli.WithGardenProfile(protocol.Deref(delegated.ProfileID), delegated.SessionID).
		SeedStandby(delegated.SeedID, client.SeedStandbyOptions{Note: standbyNote, Close: close})
}

func awaitStandbyEnded(t *testing.T, app *testworld.Peer, mark int, seed string) {
	t.Helper()
	testworld.AwaitSince(app, mark, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
		for _, s := range m.Seeds {
			if s.ID == seed && s.Standby == nil {
				return true
			}
		}
		return false
	})
}

func TestSeedMailReopensACodexTenderOnStandbyAsItselfWithItsNote(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")

	delegated := seedResumeDelegate(t, w, fakeagent.Codex, "api")
	session, seed := delegated.SessionID, delegated.SeedID
	first := w.Launched(string(session))
	first.Prompted()
	set, err := standbyAs(t, cli, delegated, true)
	if err != nil {
		t.Fatalf("seed standby --close: %v", err)
	}
	if set.Close == nil || set.Seed.Standby == nil || set.Seed.Standby.Note != standbyNote {
		t.Fatalf("seed standby --close = %+v, want the seed on standby with its note and the session closed", set)
	}
	if closed := showSession(t, cli, string(session)); protocol.Deref(closed.ClosedAt) == "" {
		t.Fatalf("after seed standby --close the ledger shows %+v, want %s closed", closed, session)
	}

	mark := len(app.Log())
	sendAgentMessage(t, cli, "sender", seed, "the pull request is approved")
	relaunched := w.Launched(string(session))
	if !relaunched.Resumed || relaunched.ConversationID != first.ConversationID {
		t.Fatalf("the wake ran %s %q, want it resuming conversation %s", relaunched.Harness, relaunched.Argv, first.ConversationID)
	}
	got := relaunched.DeliveredPrompt()
	if !strings.Contains(got, standbyNote) || !strings.Contains(got, seed) {
		t.Fatalf("the woken tender received %q, want the standby note of %s", got, seed)
	}
	if mail := seedMail(t, cli, string(session), seed); len(mail) != 1 || mail[0].Content != "the pull request is approved" {
		t.Fatalf("the woken tender's seed mailbox holds %+v, want the message", mail)
	}
	awaitStandbyEnded(t, app, mark, seed)
	if reopened := showSession(t, cli, string(session)); protocol.Deref(reopened.ClosedAt) != "" {
		t.Errorf("the ledger still shows %s closed after the wake", session)
	}
}

func TestATenderClosedBeforeReadingItsSeedMailIsResumedAgainForIt(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")

	delegated := seedResumeDelegate(t, w, fakeagent.Codex, "api")
	session, seed := delegated.SessionID, delegated.SeedID
	first := w.Launched(string(session))
	first.Prompted()
	onStandbyAndClosed(t, w, cli, delegated)

	sendAgentMessage(t, cli, "sender", seed, "the pull request is approved")
	woken := w.Launched(string(session))
	if got := woken.DeliveredPrompt(); !strings.Contains(got, standbyNote) {
		t.Fatalf("the woken tender received %q, want the standby note", got)
	}
	if shown := lifeShow(t, cli, seed); shown.Seed.Standby == nil {
		t.Fatalf("%s left standby once its mail rang, before the tender read it", seed)
	}

	closePane(app, sessionPane{session: string(session)})
	awaitClosed(app, string(session))
	again := w.Launched(string(session))
	if !again.Resumed || again.ConversationID != first.ConversationID {
		t.Fatalf("after the close the tender ran %s %q, want it resuming conversation %s", again.Harness, again.Argv, first.ConversationID)
	}
	if got := again.DeliveredPrompt(); !strings.Contains(got, standbyNote) {
		t.Fatalf("the tender woken again received %q, want the standby note", got)
	}

	mark := len(app.Log())
	if mail := seedMail(t, cli, string(session), seed); len(mail) != 1 || mail[0].Content != "the pull request is approved" {
		t.Fatalf("the tender woken again reads %+v, want the unread message", mail)
	}
	awaitStandbyEnded(t, app, mark, seed)
}

func TestAResumedCodexTenderGetsItsMailOnlyOnceItHasBooted(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")

	delegated := seedResumeDelegate(t, w, fakeagent.Codex, "api")
	session, seed := delegated.SessionID, delegated.SeedID
	first := w.Launched(string(session))
	first.Prompted()
	first.Reply("Opened the pull request. Should I wait for its review? <!-- attn:state=waiting_input -->")
	closing := testworld.AwaitSession(app, string(session), func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	if _, err := standbyAs(t, cli, delegated, true); err != nil {
		t.Fatalf("seed standby --close: %v", err)
	}

	boot := w.HoldNextBoot()
	mark := len(app.Log())
	sendAgentMessage(t, cli, "sender", seed, "the pull request is approved")
	boot.Await()
	if reopened := testworld.AwaitStateAfter(app, closing, func(protocol.Session) bool { return true }); sessionmonitor.PhaseAllows(sessionmonitor.WhenPromptReady, reopened.State) {
		t.Fatalf("the reopened tender shows %s before its agent booted, so attn would type its mail into a program not yet reading it", reopened.State)
	}
	boot.Release()
	relaunched := w.Launched(string(session))
	if got := relaunched.DeliveredPrompt(); !strings.Contains(got, standbyNote) {
		t.Fatalf("the woken Codex took %q, want the standby note once it booted", got)
	}
	seedMail(t, cli, string(session), seed)
	awaitStandbyEnded(t, app, mark, seed)
	relaunched.Reply("Reading it. <!-- attn:state=idle -->")
	nothingElsePrompted(t, app, relaunched, string(session))
}

func TestOnlySeedMailForASeedOnStandbyReopensAClosedTender(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")

	idle := seedResumeDelegate(t, w, fakeagent.Codex, "idle")
	w.Launched(string(idle.SessionID)).Prompted()
	closePane(app, sessionPane{session: string(idle.SessionID)})
	if sent := sendAgentMessage(t, cli, "sender", idle.SeedID, "for a seed that is not on standby"); sent.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("mail for a closed tender whose seed is not on standby = %+v, want it queued", sent)
	}
	stillClosed(t, cli, idle, "mail for a seed that is not on standby")

	direct := seedResumeDelegate(t, w, fakeagent.Codex, "direct")
	working := w.Launched(string(direct.SessionID))
	working.Prompted()
	if _, err := standbyAs(t, cli, direct, false); err != nil {
		t.Fatalf("seed standby: %v", err)
	}
	working.Reply("On standby. <!-- attn:state=idle -->")
	app.TypeLine(string(direct.SessionID), "keep working")
	working.Prompted()
	if sent := sendAgentMessage(t, cli, "sender", string(direct.SessionID), "to the session, not the seed"); sent.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("a session: message to the working tender = %+v, want it queued", sent)
	}
	closePane(app, sessionPane{session: string(direct.SessionID)})
	stillClosed(t, cli, direct, "a session: message to a tender whose seed is on standby")

	delegated := seedResumeDelegate(t, w, fakeagent.Codex, "api")
	session, seed := delegated.SessionID, delegated.SeedID
	first := w.Launched(string(session))
	first.Prompted()
	if _, err := standbyAs(t, cli, delegated, false); err != nil {
		t.Fatalf("seed standby: %v", err)
	}
	closePane(app, sessionPane{session: string(session)})

	mark := len(app.Log())
	sendAgentMessage(t, cli, "sender", seed, "the pull request is approved")
	relaunched := w.Launched(string(session))
	if !relaunched.Resumed || relaunched.ConversationID != first.ConversationID {
		t.Fatalf("the wake ran %s %q, want it resuming conversation %s", relaunched.Harness, relaunched.Argv, first.ConversationID)
	}
	if got := relaunched.DeliveredPrompt(); !strings.Contains(got, standbyNote) {
		t.Fatalf("the tender the user closed received %q, want the standby note", got)
	}
	seedMail(t, cli, string(session), seed)
	awaitStandbyEnded(t, app, mark, seed)
	stillClosed(t, cli, idle, "mail for a seed that is not on standby")
	stillClosed(t, cli, direct, "a session: message to a tender whose seed is on standby")
}

func TestSeedMailReachesAResumedClaudeTenderThroughItsModAfterTheFirstPoll(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")

	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	session, seed := delegated.SessionID, delegated.SeedID
	first := w.Launched(string(session))
	first.Prompted()
	w.awaitTimer(daemon.ClaudeModPollHoldTimer)
	if _, err := standbyAs(t, cli, delegated, true); err != nil {
		t.Fatalf("seed standby --close: %v", err)
	}

	boot := w.HoldNextBoot()
	mark := len(app.Log())
	sent := sendAgentMessage(t, cli, "sender", seed, "the pull request is approved")
	boot.Await()
	if sent.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("mail for the reopening tender = %+v, want it queued until the mod polls", sent)
	}
	boot.Release()
	relaunched := w.Launched(string(session))
	if !relaunched.Resumed || relaunched.ConversationID != first.ConversationID {
		t.Fatalf("the wake ran %s %q, want it resuming conversation %s", relaunched.Harness, relaunched.Argv, first.ConversationID)
	}
	got := relaunched.Prompted()
	if !relaunched.ThroughMod || !strings.Contains(got, standbyNote) {
		t.Fatalf("the woken Claude took %q (through the mod: %t), want the standby note through the mod", got, relaunched.ThroughMod)
	}
	seedMail(t, cli, string(session), seed)
	awaitStandbyEnded(t, app, mark, seed)
	nothingElsePrompted(t, app, relaunched, string(session))
}

func TestAResumedClaudeTenderWhoseModMissesItsFirstPollGetsNoTypedMail(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")

	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	session, seed := delegated.SessionID, delegated.SeedID
	w.Launched(string(session)).Prompted()
	w.awaitTimer(daemon.ClaudeModPollHoldTimer)
	if _, err := standbyAs(t, cli, delegated, true); err != nil {
		t.Fatalf("seed standby --close: %v", err)
	}

	boot := w.HoldNextBoot()
	sendAgentMessage(t, cli, "sender", seed, "the pull request is approved")
	boot.Await()
	w.awaitTimer(daemon.ClaudeModFirstPollTimer)
	beforeMiss := len(app.Log())
	w.clock.Fire(daemon.ClaudeModFirstPollTimer)
	testworld.AwaitSince(app, beforeMiss, protocol.EventNotificationsUpdated, func(protocol.NotificationsUpdatedMessage) bool { return true })
	reason := "the mod in Claude Code " + fakeagent.ClaudeVersion + " never polled"
	resumeBlockedNotice(t, app, cli, seed, reason)
	if later := sendAgentMessage(t, cli, "sender", seed, "and the checks passed"); later.Status != protocol.AgentMsgStatusQueued || !strings.Contains(later.Detail, "first poll") {
		t.Fatalf("mail after the mod missed its first poll = %+v, want it queued for the mod", later)
	}

	mark := len(app.Log())
	boot.Release()
	relaunched := w.Launched(string(session))
	got := relaunched.Prompted()
	if !relaunched.ThroughMod || !strings.Contains(got, standbyNote) {
		t.Fatalf("the woken Claude took %q (through the mod: %t), want the standby note through the mod once it polls", got, relaunched.ThroughMod)
	}
	seedMail(t, cli, string(session), seed)
	awaitStandbyEnded(t, app, mark, seed)
	nothingElsePrompted(t, app, relaunched, string(session))
}

func TestAnAnswerTheModHoldsKeepsTheSeedOnStandbyUntilClaudeTakesIt(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")

	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	session, seed := string(delegated.SessionID), delegated.SeedID
	run := w.Launched(session)
	run.Prompted()
	w.awaitTimer(daemon.ClaudeModPollHoldTimer)
	if _, err := standbyAs(t, cli, delegated, false); err != nil {
		t.Fatalf("seed standby: %v", err)
	}
	workingOn(t, app, run, session, "write the essay")

	asked, err := cli.AskOpen(delegated.SessionID, seed, cacheAsk, "", w.Path("api"))
	if err != nil {
		t.Fatalf("ask about %s: %v", seed, err)
	}
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 1 })
	answerCacheAsk(t, app, asked.ID)
	modEvent(t, run, fakeagent.ModEventHeld, cacheAnswerText)
	if shown := lifeShow(t, cli, seed); shown.Seed.Standby == nil {
		t.Fatalf("%s left standby while the mod held its answer", seed)
	}

	stoppedByEsc(t, app, run, session)
	modEvent(t, run, fakeagent.ModEventRefusedStop, cacheAnswerText)
	if shown := lifeShow(t, cli, seed); shown.Seed.Standby == nil {
		t.Fatalf("%s left standby after Esc returned its answer unread", seed)
	}

	mark := len(app.Log())
	workingOn(t, app, run, session, "carry on")
	modEvent(t, run, fakeagent.ModEventHeld, cacheAnswerText)
	if appended := run.ModToolBoundary(); len(appended) != 1 || !strings.Contains(appended[0], cacheAnswerText) {
		t.Fatalf("the tool boundary appended %v, want the answer", appended)
	}
	awaitStandbyEnded(t, app, mark, seed)
}

func TestAnAnswerReturnedBeforeItsPlacementAnswersStaysUnreadOnStandby(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")

	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	session, seed := string(delegated.SessionID), delegated.SeedID
	run := w.Launched(session)
	run.Prompted()
	w.awaitTimer(daemon.ClaudeModPollHoldTimer)
	if _, err := standbyAs(t, cli, delegated, false); err != nil {
		t.Fatalf("seed standby: %v", err)
	}
	workingOn(t, app, run, session, "write the essay")

	asked, err := cli.AskOpen(delegated.SessionID, seed, cacheAsk, "", w.Path("api"))
	if err != nil {
		t.Fatalf("ask about %s: %v", seed, err)
	}
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 1 })
	answering := w.PauseAt(pausepoint.RingPlacementAnswering)
	answerCacheAsk(t, app, asked.ID)
	answering.Await()
	modEvent(t, run, fakeagent.ModEventHeld, cacheAnswerText)
	stoppedByEsc(t, app, run, session)
	answering.Release()
	modEvent(t, run, fakeagent.ModEventRefusedStop, cacheAnswerText)
	if shown := lifeShow(t, cli, seed); shown.Seed.Standby == nil {
		t.Fatalf("%s left standby though the mod returned its answer before the placement answered", seed)
	}

	mark := len(app.Log())
	workingOn(t, app, run, session, "carry on")
	modEvent(t, run, fakeagent.ModEventHeld, cacheAnswerText)
	if appended := run.ModToolBoundary(); len(appended) != 1 || !strings.Contains(appended[0], cacheAnswerText) {
		t.Fatalf("the tool boundary appended %v, want the answer", appended)
	}
	awaitStandbyEnded(t, app, mark, seed)
}

func TestAClaudeTenderWhoseModIsGoneGoesOnStandbyAndItsResumeLoadsTheMod(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")
	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	run := w.Launched(string(delegated.SessionID))
	run.Prompted()
	typedClaude(w, run)

	if _, err := standbyAs(t, cli, delegated, true); err != nil {
		t.Fatalf("seed standby --close from a Claude session whose mod is gone: %v", err)
	}
	mark := len(app.Log())
	sendAgentMessage(t, cli, "sender", delegated.SeedID, "the pull request is approved")
	relaunched := w.Launched(string(delegated.SessionID))
	if got := relaunched.Prompted(); !relaunched.ThroughMod || !strings.Contains(got, standbyNote) {
		t.Fatalf("the resumed Claude took %q (through the mod: %t), want the standby note through the mod", got, relaunched.ThroughMod)
	}
	seedMail(t, cli, string(delegated.SessionID), delegated.SeedID)
	awaitStandbyEnded(t, app, mark, delegated.SeedID)
}

func TestStandbyCloseWaitsUntilTheTenderHasReadItsSeedMail(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	cli := w.Client()
	registerSessions(t, w, cli, "sender")
	delegated := seedResumeDelegate(t, w, fakeagent.Codex, "api")
	session, seed := delegated.SessionID, delegated.SeedID
	w.Launched(string(session)).Prompted()
	sendAgentMessage(t, cli, "sender", seed, "read me before you close")

	_, err := standbyAs(t, cli, delegated, true)
	if err == nil || !strings.Contains(err.Error(), "attn agent inbox") {
		t.Fatalf("seed standby --close with unread seed mail = %v, want a refusal naming attn agent inbox", err)
	}
	if shown := lifeShow(t, cli, seed); shown.Seed.Standby != nil {
		t.Fatalf("the refused standby left %+v on the seed", shown.Seed.Standby)
	}
	if open := showSession(t, cli, string(session)); protocol.Deref(open.ClosedAt) != "" {
		t.Fatalf("the refused standby closed %s", session)
	}

	if mail := seedMail(t, cli, string(session), seed); len(mail) != 1 || mail[0].Content != "read me before you close" {
		t.Fatalf("the tender's seed mailbox holds %+v, want the message", mail)
	}
	set, err := standbyAs(t, cli, delegated, true)
	if err != nil || set.Close == nil || set.Seed.Standby == nil {
		t.Fatalf("seed standby --close after reading = %+v, %v, want the seed on standby and the session closed", set, err)
	}
	if closed := showSession(t, cli, string(session)); protocol.Deref(closed.ClosedAt) == "" {
		t.Fatalf("after reading, seed standby --close left %s open", session)
	}
}

func remindAs(t *testing.T, cli *client.Client, delegated *protocol.DelegateResult, in, message string) {
	t.Helper()
	if _, err := cli.WithGardenProfile(protocol.Deref(delegated.ProfileID), delegated.SessionID).
		SeedReminder(delegated.SeedID, client.SeedReminderOptions{In: in, Message: message}); err != nil {
		t.Fatalf("seed remind %s: %v", delegated.SeedID, err)
	}
}

func onStandbyAndClosed(t *testing.T, w *world, cli *client.Client, delegated *protocol.DelegateResult) {
	t.Helper()
	if _, err := standbyAs(t, cli, delegated, true); err != nil {
		t.Fatalf("seed standby --close %s: %v", delegated.SeedID, err)
	}
	if closed := showSession(t, cli, string(delegated.SessionID)); protocol.Deref(closed.ClosedAt) == "" {
		t.Fatalf("seed standby --close left %s open", delegated.SessionID)
	}
}

func stillClosed(t *testing.T, cli *client.Client, delegated *protocol.DelegateResult, what string) {
	t.Helper()
	if entry := showSession(t, cli, string(delegated.SessionID)); protocol.Deref(entry.ClosedAt) == "" {
		t.Fatalf("%s reopened the tender of %s", what, delegated.SeedID)
	}
}

func seedPullRequestReadiness(t *testing.T, cli *client.Client, seed string) string {
	t.Helper()
	watched, err := cli.SeedPullRequests("sender", seed)
	if err != nil {
		t.Fatalf("pull requests watched for %s: %v", seed, err)
	}
	if len(watched) != 1 {
		return ""
	}
	return protocol.Deref(watched[0].ReadinessState)
}

func TestTheTenderResumeSettingDecidesWhichSeedMailResumesATenderOnStandby(t *testing.T) {
	serveWatchedPullRequest(t, watchedPullRequestGitHub{state: "OPEN"})
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")
	recordPullRequest(t, cli, "sender", watchedPullRequestURL)
	runs := map[string]*fakeagent.Run{}
	launch := func(dir string) *protocol.DelegateResult {
		delegated := seedResumeDelegate(t, w, fakeagent.Codex, dir)
		run := w.Launched(string(delegated.SessionID))
		run.Prompted()
		runs[delegated.SeedID] = run
		return delegated
	}
	peerUnderWatches := launch("peer-under-watches")
	underOff := launch("under-off")
	reminderUnderWatches := launch("reminder-under-watches")
	pullRequestUnderOff := launch("pull-request-under-off")
	pullRequestUnderWatches := launch("pull-request-under-watches")
	pullRequestUnderAny := launch("pull-request-under-any")
	remindAs(t, cli, underOff, "5m", "Check the rollout")
	remindAs(t, cli, reminderUnderWatches, "12m", "Check the canary")
	for _, delegated := range []*protocol.DelegateResult{peerUnderWatches, underOff, reminderUnderWatches, pullRequestUnderOff, pullRequestUnderWatches, pullRequestUnderAny} {
		onStandbyAndClosed(t, w, cli, delegated)
	}
	pullRequestReadyFor := func(delegated *protocol.DelegateResult) func() bool {
		if err := cli.WatchSessionPullRequest("sender", delegated.SeedID, watchedPullRequestURL, protocol.PullRequestWatchModeGreen, ""); err != nil {
			t.Fatalf("watch the pull request for %s: %v", delegated.SeedID, err)
		}
		return func() bool { return seedPullRequestReadiness(t, cli, delegated.SeedID) == "ready" }
	}
	woken := func(delegated *protocol.DelegateResult, by string) []protocol.AgentInboxItem {
		t.Helper()
		run := w.Launched(string(delegated.SessionID))
		if first := runs[delegated.SeedID]; !run.Resumed || run.ConversationID != first.ConversationID {
			t.Fatalf("%s ran %s %q, want it resuming conversation %s", by, run.Harness, run.Argv, first.ConversationID)
		}
		if got := run.DeliveredPrompt(); !strings.Contains(got, standbyNote) {
			t.Fatalf("the tender woken by %s received %q, want its standby note", by, got)
		}
		return seedMail(t, cli, string(delegated.SessionID), delegated.SeedID)
	}
	pullRequestNotice := func(m protocol.AgentInboxItem) bool {
		return strings.Contains(m.Content, "Pull request monitor: ready")
	}

	setSetting(t, app, "garden.tender_resume", "watches")
	if sent := sendAgentMessage(t, cli, "sender", peerUnderWatches.SeedID, "a peer message"); !strings.Contains(sent.Detail, "garden.tender_resume=watches") {
		t.Fatalf("a peer message under watches = %+v, want it queued naming the setting", sent)
	}
	stillClosed(t, cli, peerUnderWatches, "a peer message under garden.tender_resume=watches")

	setSetting(t, app, "garden.tender_resume", "off")
	readyUnderOff := pullRequestReadyFor(pullRequestUnderOff)
	w.tickSchedulesUntil("the first reminder fires and the pull request update arrives", func() bool {
		return lifeShow(t, cli, underOff.SeedID).Seed.Reminder == nil && readyUnderOff()
	})
	for _, delegated := range []*protocol.DelegateResult{underOff, pullRequestUnderOff} {
		if sent := sendAgentMessage(t, cli, "sender", delegated.SeedID, "a peer message"); !strings.Contains(sent.Detail, "garden.tender_resume=off") {
			t.Fatalf("a peer message to %s after its notice under off = %+v, want it queued naming the setting", delegated.SeedID, sent)
		}
		stillClosed(t, cli, delegated, "a notice and a peer message under garden.tender_resume=off")
	}

	setSetting(t, app, "garden.tender_resume", "watches")
	w.tickSchedulesUntil("the second reminder fires", func() bool { return lifeShow(t, cli, reminderUnderWatches.SeedID).Seed.Reminder == nil })
	if mail := woken(reminderUnderWatches, "the reminder under watches"); len(mail) != 1 || protocol.Deref(mail[0].SourceID) != "seed-reminder" {
		t.Fatalf("the tender woken under watches holds %+v, want its reminder", mail)
	}
	w.tickSchedulesUntil("the pull request update arrives under watches", pullRequestReadyFor(pullRequestUnderWatches))
	if mail := woken(pullRequestUnderWatches, "the pull request update under watches"); len(mail) != 1 || !pullRequestNotice(mail[0]) {
		t.Fatalf("the tender woken under watches holds %+v, want the pull request update", mail)
	}

	setSetting(t, app, "garden.tender_resume", "any")
	w.tickSchedulesUntil("the pull request update arrives under any", pullRequestReadyFor(pullRequestUnderAny))
	if mail := woken(pullRequestUnderAny, "the pull request update under any"); len(mail) != 1 || !pullRequestNotice(mail[0]) {
		t.Fatalf("the tender woken under any holds %+v, want the pull request update", mail)
	}
	if _, err := standbyAs(t, cli, reminderUnderWatches, true); err != nil {
		t.Fatalf("seed standby --close again: %v", err)
	}
	sendAgentMessage(t, cli, "sender", reminderUnderWatches.SeedID, "a peer message under any")
	if mail := woken(reminderUnderWatches, "a peer message under any"); len(mail) != 1 || mail[0].Content != "a peer message under any" {
		t.Fatalf("the tender woken under any holds %+v, want the peer message", mail)
	}
}

func resumeBlockedNotice(t *testing.T, app *testworld.Peer, cli *client.Client, seed, reason string) {
	t.Helper()
	notes := lifeShow(t, cli, seed).Notes
	if !slices.ContainsFunc(notes, func(n protocol.SeedNote) bool { return strings.Contains(n.Body, reason) }) {
		t.Errorf("the notes of %s are %+v, want one naming %q", seed, notes, reason)
	}
	listed := listNotifications(app).Notifications
	opens := slices.IndexFunc(listed, func(n protocol.Notification) bool {
		return n.Severity == protocol.NotificationSeverityCritical && strings.Contains(n.Body, reason) &&
			slices.ContainsFunc(n.Actions, func(a protocol.NotificationAction) bool { return a.Kind == "open_seed" && a.TargetID == seed })
	})
	if opens < 0 {
		t.Errorf("the notifications are %+v, want a critical one naming %q that opens %s", listed, reason, seed)
	}
}

func TestTheFortyNinthTenderResumeInADayIsRefusedLoudlyAndTheMailWaitsForTheWindow(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.App(), w.Client()
		setSetting(t, app, "crew.heartbeat_enabled", "false")
		setSetting(t, app, "crew.away_close_enabled", "false")
		registerSessions(t, w, cli, "sender")
		seed := plantSeedAs(t, cli, "sender", "review the build")
		if _, err := cli.SeedTransition("", seed, "tend", "", false, client.SeedTransitionOptions{Assignee: "trellis"}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		first := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
		w.bootBubbleClaude(t, string(first)).reply("Ready. <!-- attn:state=idle -->")
		if _, err := cli.CrewHandoff(first, "Sleep until seed mail", false); err != nil {
			t.Fatal(err)
		}

		for wake := 1; wake <= 48; wake++ {
			sent := sendAgentMessage(t, cli, "sender", seed, fmt.Sprintf("update %d", wake))
			if sent.TargetSessionID == "" {
				t.Fatalf("wake %d of 48 = %+v, want the tender woken", wake, sent)
			}
			day := w.bootBubbleClaude(t, string(sent.TargetSessionID))
			day.reply("Ready. <!-- attn:state=idle -->")
			readInbox(t, cli, day.id, 0)
			if _, err := cli.CrewHandoff(sent.TargetSessionID, "Sleep until seed mail", false); err != nil {
				t.Fatal(err)
			}
			w.advance(5 * time.Minute)
		}

		refused := sendAgentMessage(t, cli, "sender", seed, "update 49")
		if refused.TargetSessionID != "" || !strings.Contains(refused.Detail, "tender_resume_tripwire") {
			t.Fatalf("the 49th wake within a day = %+v, want it refused by the tripwire", refused)
		}
		resumeBlockedNotice(t, app, cli, seed, "tender_resume_tripwire")
		if bound := crewRosterMember(t, cli, "trellis").BindingSession; bound != nil {
			t.Fatalf("the tripwire still woke %s", *bound)
		}

		w.advance(24*time.Hour - 48*5*time.Minute - time.Second)
		if bound := crewRosterMember(t, cli, "trellis").BindingSession; bound != nil {
			t.Fatalf("the mail woke %s a second before the window reopened", *bound)
		}
		w.advance(time.Second)
		day := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
		if day == "" {
			t.Fatal("the mail never woke the tender after the window reopened")
		}
		w.bootBubbleClaude(t, string(day)).reply("Ready. <!-- attn:state=idle -->")
		if mail := seedMail(t, cli, string(day), seed); len(mail) != 1 || mail[0].Content != "update 49" {
			t.Fatalf("after the window reopened the tender holds %+v, want update 49", mail)
		}
	})
}

func TestSeedMailResumesACrewMemberTenderOnStandbyWithItsNote(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.App(), w.Client()
		setSetting(t, app, "crew.heartbeat_enabled", "false")
		setSetting(t, app, "crew.away_close_enabled", "false")
		registerSessions(t, w, cli, "sender")
		seed := plantSeedAs(t, cli, "sender", "review the build")
		if _, err := cli.SeedTransition("", seed, "tend", "", false, client.SeedTransitionOptions{Assignee: "trellis"}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		first := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
		w.bootBubbleClaude(t, string(first)).reply("Ready. <!-- attn:state=idle -->")
		readInbox(t, cli, string(first), 0)
		if _, err := cli.WithGardenProfile("", first).SeedStandby(seed, client.SeedStandbyOptions{Note: standbyNote}); err != nil {
			t.Fatalf("the member puts %s on standby: %v", seed, err)
		}
		if _, err := cli.CrewHandoff(first, "Sleep until seed mail", false); err != nil {
			t.Fatal(err)
		}

		sent := sendAgentMessage(t, cli, "sender", seed, "the pull request is approved")
		if sent.TargetSessionID == "" || sent.TargetSessionID == first {
			t.Fatalf("seed send = %+v, want the member woken in a new day", sent)
		}
		day := w.bootBubbleClaude(t, string(sent.TargetSessionID))
		day.reply("Ready. <!-- attn:state=idle -->")
		if day.promptsContaining(standbyNote) != 1 || day.promptsContaining(seed) == 0 {
			t.Fatalf("the woken member took %q, want the standby note of %s once", day.prompts(), seed)
		}
		if mail := seedMail(t, cli, day.id, seed); len(mail) != 1 || mail[0].Content != "the pull request is approved" {
			t.Fatalf("the woken member's seed mailbox holds %+v, want the message", mail)
		}
		if standby := lifeShow(t, cli, seed).Seed.Standby; standby != nil {
			t.Errorf("after the woken member read its mail %s still shows standby %+v", seed, standby)
		}
	})
}

func TestATenderResumeRefusedForARemovedWorktreeNotesAndNotifiesTheReason(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")
	delegated := seedResumeDelegate(t, w, fakeagent.Codex, "api")
	w.Launched(string(delegated.SessionID)).Prompted()
	onStandbyAndClosed(t, w, cli, delegated)
	if err := os.RemoveAll(delegated.Directory); err != nil {
		t.Fatal(err)
	}

	reason := "the original directory no longer exists"
	mark := len(app.Log())
	if sent := sendAgentMessage(t, cli, "sender", delegated.SeedID, "the pull request is approved"); !strings.Contains(sent.Detail, reason) {
		t.Fatalf("mail for a tender whose worktree is gone = %+v, want it queued naming %q", sent, reason)
	}
	stillClosed(t, cli, delegated, "mail for a removed worktree")
	resumeBlockedNotice(t, app, cli, delegated.SeedID, reason)
	testworld.AwaitSince(app, mark, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
		return slices.ContainsFunc(m.Seeds, func(s protocol.Seed) bool {
			return s.ID == delegated.SeedID && s.Standby != nil && strings.Contains(protocol.Deref(s.Standby.ResumeBlocked), reason)
		})
	})
	if standby := lifeShow(t, cli, delegated.SeedID).Seed.Standby; standby == nil || !strings.Contains(protocol.Deref(standby.ResumeBlocked), reason) {
		t.Errorf("the seed's standby is %+v, want resume_blocked naming %q", standby, reason)
	}
}

func TestStandbyEndsWhenTheSeedMovesAndOutlivesAnOlderBuildRewritingTheSeed(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	cli := w.Client()
	registerSessions(t, w, cli, "sender")
	onStandby := func(dir string) *protocol.DelegateResult {
		delegated := seedResumeDelegate(t, w, fakeagent.Codex, dir)
		w.Launched(string(delegated.SessionID)).Prompted()
		if _, err := standbyAs(t, cli, delegated, false); err != nil {
			t.Fatalf("seed standby %s: %v", delegated.SeedID, err)
		}
		return delegated
	}
	kept := onStandby("kept")
	for _, move := range []struct{ verb, reason string }{{"park", "waiting on review"}, {"harvest", "shipped"}, {"wither", ""}} {
		delegated := onStandby(move.verb)
		lifeMove(t, cli, string(delegated.SessionID), delegated.SeedID, move.verb, move.reason, "")
		if standby := lifeShow(t, cli, delegated.SeedID).Seed.Standby; standby != nil {
			t.Errorf("after %s, %s still shows standby %+v", move.verb, delegated.SeedID, standby)
		}
	}
	retended := onStandby("retended")
	if _, err := cli.SeedTransition("sender", retended.SeedID, "tend", "", true, client.SeedTransitionOptions{}); err != nil {
		t.Fatalf("sender takes over %s: %v", retended.SeedID, err)
	}
	if standby := lifeShow(t, cli, retended.SeedID).Seed.Standby; standby != nil {
		t.Errorf("after a new tender took %s it still shows standby %+v", retended.SeedID, standby)
	}
	if _, err := cli.SeedTransition(retended.SessionID, retended.SeedID, "tend", "", true, client.SeedTransitionOptions{}); err != nil {
		t.Fatalf("the first tender takes %s back: %v", retended.SeedID, err)
	}
	if standby := lifeShow(t, cli, retended.SeedID).Seed.Standby; standby != nil {
		t.Errorf("after its first tender took %s back it shows standby %+v again", retended.SeedID, standby)
	}

	w.stop()
	older, err := store.NewWithDB(config.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	schema, found, err := older.DocumentCollection(garden.Namespace, garden.CollectionSeeds)
	if err != nil || !found {
		t.Fatalf("the seeds collection = %v, %v", found, err)
	}
	doc, _, err := older.GetDocument(*schema, kept.SeedID)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(doc.Body, &body); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"standby", "standby_at"} {
		delete(body, field)
	}
	body["title"] = "Renamed by the older build"
	rewritten, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := older.PutDocument(*schema, kept.SeedID, rewritten, time.Now(), &doc.Rev); err != nil {
		t.Fatal(err)
	}
	if err := older.Close(); err != nil {
		t.Fatal(err)
	}
	w.start()

	if standby := lifeShow(t, w.Client(), kept.SeedID).Seed.Standby; standby == nil || standby.Note != standbyNote {
		t.Fatalf("after an older build rewrote %s it shows standby %+v, want it still on standby with its note", kept.SeedID, standby)
	}
}

func TestAClosedTenderHoldsOnlyItsSeedOnStandby(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	cli := w.Client()
	registerSessions(t, w, cli, "sender")
	delegated := seedResumeDelegate(t, w, fakeagent.Codex, "api")
	w.Launched(string(delegated.SessionID)).Prompted()
	seed, tender := delegated.SeedID, string(delegated.SessionID)
	planted, err := cli.SeedPlant(delegated.SessionID, "Follow-up the tender also tends", "", "", "")
	if err != nil {
		t.Fatalf("the tender plants a second seed: %v", err)
	}
	other := planted.Seed.ID
	lifeMove(t, cli, tender, other, "tend", "", "")
	onStandbyAndClosed(t, w, cli, delegated)

	ready, err := cli.SeedReady("sender", "", true)
	if err != nil {
		t.Fatalf("seed ready: %v", err)
	}
	listed := map[string]bool{}
	for _, row := range slices.Concat(ready.Seeds, ready.Plots) {
		listed[row.ID] = true
	}
	if listed[seed] {
		t.Fatalf("seed ready lists %s while it is on standby for its closed tender", seed)
	}
	if !listed[other] {
		t.Fatalf("seed ready leaves out %s, which its closed tender tends without standby", other)
	}
	if tended := lifeMove(t, cli, "sender", other, "tend", "", ""); tended.Tender == nil || !strings.Contains(string(tended.Tender.Ref), "sender") {
		t.Fatalf("after sender tends %s without --force it is tended by %+v, want sender", other, tended.Tender)
	}
	if _, err := cli.SeedTransition("sender", seed, "tend", "", false, client.SeedTransitionOptions{}); err == nil || !strings.Contains(err.Error(), tender) {
		t.Fatalf("an unrelated session tending %s got %v, want a refusal naming its tender %s", seed, err, tender)
	}
	if _, err := cli.WithGardenProfile(protocol.Deref(delegated.ProfileID), "sender").
		SeedStandby(seed, client.SeedStandbyOptions{Clear: true}); err == nil {
		t.Fatalf("an unrelated session cleared the standby of %s", seed)
	}
	if standby := lifeShow(t, cli, seed).Seed.Standby; standby == nil {
		t.Fatalf("the refused moves ended the standby of %s", seed)
	}

	if _, err := cli.SeedTransition("sender", seed, "tend", "", true, client.SeedTransitionOptions{}); err != nil {
		t.Fatalf("an unrelated session tending %s with --force: %v", seed, err)
	}
	shown := lifeShow(t, cli, seed).Seed
	if shown.Standby != nil {
		t.Errorf("after a forced takeover %s still shows standby %+v", seed, shown.Standby)
	}
	if shown.Tender == nil || !strings.Contains(string(shown.Tender.Ref), "sender") {
		t.Errorf("after a forced takeover %s is tended by %+v, want sender", seed, shown.Tender)
	}
}

func TestTheSessionAboveAClosedTenderOnStandbyMovesItsSeedWithoutForce(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	cli := w.Client()
	registerSessions(t, w, cli, "parent")
	cwd := w.Path("api")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	w.SetCodexFolderTrust(cwd, "trusted")
	request := brief(cwd, "Investigate the tracked task.")
	request.Agent = protocol.Ptr(string(fakeagent.Codex))
	request.SourceSessionID = protocol.Ptr(protocol.SessionID("parent"))
	delegated, err := cli.Delegate(request)
	if err != nil {
		t.Fatalf("parent delegates: %v", err)
	}
	w.Launched(string(delegated.SessionID)).Prompted()
	onStandbyAndClosed(t, w, cli, delegated)

	parked := lifeMove(t, cli, "parent", delegated.SeedID, "park", "", "")
	if parked.Status != garden.StatusParked {
		t.Fatalf("the parent parked %s into %q", delegated.SeedID, parked.Status)
	}
}

func standbyPullRequests(m protocol.GardenSeedsChangedMessage, seed string, want func([]protocol.SeedWatchedPullRequest) bool) bool {
	for _, s := range m.Seeds {
		if s.ID == seed && s.Standby != nil && want(s.Standby.PullRequests) {
			return true
		}
	}
	return false
}

func TestSeedsOnStandbyListWithTheirPullRequestWatchesAndTheGardenFollowsTheWatch(t *testing.T) {
	gh := serveWatchedPullRequest(t, watchedPullRequestGitHub{state: "OPEN", mergeStateStatus: "BLOCKED"})
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setSetting(t, app, "garden.tender_resume", "off")
		watcher := w.Spawn(app, fakeagent.Codex, w.Path("watcher"))
		tender := w.Spawn(app, fakeagent.Codex, w.Path("tender"))
		onStandby := plantSeedAs(t, cli, tender, "Merge the readiness check once approved")
		moveDelegationSeed(t, cli, tender, onStandby, "tend", "")
		growing := plantSeedAs(t, cli, watcher, "Ship the readiness check")
		moveDelegationSeed(t, cli, watcher, growing, "tend", "")
		asTender := cli.WithGardenProfile("", protocol.SessionID(tender))
		if _, err := asTender.SeedReminder(onStandby, client.SeedReminderOptions{In: "20m", Message: "Check the canary"}); err != nil {
			t.Fatalf("seed remind %s: %v", onStandby, err)
		}
		if _, err := asTender.SeedStandby(onStandby, client.SeedStandbyOptions{Note: standbyNote, Close: true}); err != nil {
			t.Fatalf("seed standby --close %s: %v", onStandby, err)
		}
		watchPullRequestAs(t, cli, watcher, growing, protocol.PullRequestWatchModeGreen, "")
		readiness := func() string { return protocol.Deref(onlySeedPullRequest(t, cli, watcher, onStandby).ReadinessState) }

		mark := len(app.Log())
		watchPullRequestAs(t, cli, watcher, onStandby, protocol.PullRequestWatchModeGreen, "")
		testworld.AwaitSince(app, mark, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
			return standbyPullRequests(m, onStandby, func(prs []protocol.SeedWatchedPullRequest) bool {
				return len(prs) == 1 && prs[0].URL == watchedPullRequestURL
			})
		})
		w.advance(time.Second)
		if got := readiness(); got != "blocked" {
			t.Fatalf("the watch of %s reads %q, want the pull request blocked", onStandby, got)
		}

		mark = len(app.Log())
		gh.set(func(gh *watchedPullRequestGitHub) { gh.mergeStateStatus = "CLEAN" })
		w.advance(protocol.HeatHotInterval)
		if got := readiness(); got != "ready" {
			t.Fatalf("the watch of %s reads %q after the pull request cleared, want it ready", onStandby, got)
		}
		testworld.AwaitSince(app, mark, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
			return standbyPullRequests(m, onStandby, func(prs []protocol.SeedWatchedPullRequest) bool {
				return len(prs) == 1 && prs[0].ReadinessState == "ready"
			})
		})

		listed, err := cli.SeedListStandby(protocol.SessionID(watcher))
		if err != nil {
			t.Fatalf("seed ls --standby: %v", err)
		}
		if len(listed.Seeds) != 1 || listed.Total != 1 || listed.Seeds[0].ID != onStandby {
			t.Fatalf("seed ls --standby = %+v, want only %s and not %s, whose pull request watch does not put it on standby", listed.Seeds, onStandby, growing)
		}
		row := listed.Seeds[0]
		if row.Standby == nil || row.Standby.Note != standbyNote {
			t.Fatalf("the standby row of %s = %+v, want its note", row.ID, row.Standby)
		}
		if prs := row.Standby.PullRequests; len(prs) != 1 || prs[0].PRID == "" || prs[0].URL != watchedPullRequestURL || prs[0].State != "open" || prs[0].ReadinessState != "ready" {
			t.Fatalf("the standby row of %s watches %+v, want the open pull request read ready", row.ID, prs)
		}
		if row.Reminder == nil || row.Reminder.Message != "Check the canary" {
			t.Fatalf("the standby row of %s carries reminder %+v, want its reminder", row.ID, row.Reminder)
		}

		mark = len(app.Log())
		if err := cli.UnwatchSessionPullRequest(protocol.SessionID(watcher), onStandby, watchedPullRequestURL); err != nil {
			t.Fatalf("unwatch the pull request for %s: %v", onStandby, err)
		}
		testworld.AwaitSince(app, mark, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
			return standbyPullRequests(m, onStandby, func(prs []protocol.SeedWatchedPullRequest) bool { return len(prs) == 0 })
		})

		synctest.Wait()
		mark = len(app.Log())
		gh.set(func(gh *watchedPullRequestGitHub) { gh.mergeStateStatus = "BLOCKED" })
		w.advance(protocol.HeatHotInterval)
		if got := protocol.Deref(onlySeedPullRequest(t, cli, watcher, growing).ReadinessState); got != "blocked" {
			t.Fatalf("the watch of %s reads %q after the pull request was blocked again, want it blocked", growing, got)
		}
		synctest.Wait()
		if pushes := gardenPushesSince(app, mark); pushes != 0 {
			t.Fatalf("a pull request watched only by %s, which is not on standby, pushed the Garden %d times while %s is on standby", growing, pushes, onStandby)
		}
	})
}

func gardenPushesSince(app *testworld.Peer, mark int) int {
	pushes := 0
	for _, raw := range app.Log()[mark:] {
		var event protocol.WebSocketEvent
		if json.Unmarshal(raw, &event) == nil && event.Event == protocol.EventGardenSeedsChanged {
			pushes++
		}
	}
	return pushes
}

func pluginTenderOnStandby(t *testing.T, w *world, capabilities map[string]bool) (*driverPeer, *protocol.DelegateResult) {
	t.Helper()
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")
	driver := connectDriver(t, w, "snipe-plugin", "snipe", capabilities)
	awaitDriverAvailable(app, "snipe")
	delegations := make(chan *protocol.DelegateResult, 1)
	go func() { delegations <- seedResumeDelegate(t, w, scriptedAgent, "tender") }()
	run := driver.launched()
	delegated := <-delegations
	driver.mustReport("session.report_metadata", map[string]any{
		"session_id": run.SessionID, "run_id": run.RunID, "seq": 1,
		"metadata": map[string]string{"native_id": "saved-conversation"}, "resume_session_id": "saved-conversation",
	})
	return driver, delegated
}

type resumeStorage struct {
	stalled   atomic.Bool
	available atomic.Bool
	stall     chan struct{}
}

func serveResumeStorage(t *testing.T, driver *driverPeer) *resumeStorage {
	t.Helper()
	storage := &resumeStorage{stall: make(chan struct{}, 1)}
	storage.available.Store(true)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			select {
			case <-done:
				return
			case request := <-driver.requests:
				if storage.stalled.Load() {
					storage.stall <- struct{}{}
					continue
				}
				var check resumeInspection
				if err := json.Unmarshal(request.Params, &check); err != nil {
					t.Error(err)
					return
				}
				available := storage.available.Load()
				answerResumeInspection(driver, request.ID, check, func(string, string) bool { return available }, "the plugin's storage lost it")
			}
		}
	}()
	return storage
}

func noResumeBlocked(t *testing.T, app *testworld.Peer, cli *client.Client, seed string) {
	t.Helper()
	if standby := lifeShow(t, cli, seed).Seed.Standby; standby == nil || protocol.Deref(standby.ResumeBlocked) != "" {
		t.Fatalf("the standby of %s is %+v, want it on standby with no blocked wake", seed, standby)
	}
	if slices.ContainsFunc(listNotifications(app).Notifications, func(n protocol.Notification) bool { return n.Severity == protocol.NotificationSeverityCritical }) {
		t.Fatal("a failure that can fix itself raised a critical notification")
	}
}

func retriedResumeDeliversTheOriginalMail(t *testing.T, w *world, driver *driverPeer, delegated *protocol.DelegateResult, retried driverLaunch) {
	t.Helper()
	session, seed := string(delegated.SessionID), delegated.SeedID
	if retried.Method != "driver.resume" || retried.ResumeSessionID != "saved-conversation" {
		t.Fatalf("the retry was %+v, want it resuming saved-conversation", retried)
	}
	if err := driver.state(retried, 1, "waiting_input"); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
	if typed := strings.Join(w.terminal(session).Submitted(), "\n"); !strings.Contains(typed, standbyNote) {
		t.Fatalf("the woken tender received %q, want the standby note", typed)
	}
	if mail := seedMail(t, w.Client(), session, seed); len(mail) != 1 || mail[0].Content != "the pull request is approved" {
		t.Fatalf("the woken tender reads %+v, want the original mail", mail)
	}
}

func TestATenderResumeThatFailsToLaunchRetriesAndDeliversTheOriginalMail(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		driver, delegated := pluginTenderOnStandby(t, w, map[string]bool{"resume": true, "initial_prompt": true, "state_reporting": true})
		onStandbyAndClosed(t, w, cli, delegated)

		w.terms.RefuseNextSpawn(errors.New("PTY temporarily unavailable"))
		sent := sendAgentMessage(t, cli, "sender", delegated.SeedID, "the pull request is approved")
		if !strings.Contains(sent.Detail, "PTY temporarily unavailable") {
			t.Fatalf("mail during the outage = %+v, want it queued naming the outage", sent)
		}
		driver.launched()
		synctest.Wait()
		stillClosed(t, cli, delegated, "a failed launch")
		noResumeBlocked(t, app, cli, delegated.SeedID)

		w.advance(5 * time.Minute)
		retriedResumeDeliversTheOriginalMail(t, w, driver, delegated, driver.launched())
	})
}

func TestATenderResumeWhoseInspectionFailsRetriesAndDeliversTheOriginalMail(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		driver, delegated := pluginTenderOnStandby(t, w, map[string]bool{"resume": true, "resume_availability": true, "initial_prompt": true, "state_reporting": true})
		storage := serveResumeStorage(t, driver)
		onStandbyAndClosed(t, w, cli, delegated)

		storage.stalled.Store(true)
		sending := make(chan *protocol.AgentMsgResult, 1)
		go func() {
			sending <- sendAgentMessage(t, cli, "sender", delegated.SeedID, "the pull request is approved")
		}()
		<-storage.stall
		w.advance(10 * time.Second)
		if sent := <-sending; !strings.Contains(sent.Detail, "resume_availability_timeout") {
			t.Fatalf("mail during the outage = %+v, want it queued naming the timeout", sent)
		}
		storage.stalled.Store(false)
		synctest.Wait()
		stillClosed(t, cli, delegated, "a failed inspection")
		noResumeBlocked(t, app, cli, delegated.SeedID)

		w.advance(5 * time.Minute)
		retriedResumeDeliversTheOriginalMail(t, w, driver, delegated, driver.launched())
	})
}

func TestATenderResumeForAConversationThePluginReportsGoneStaysBlockedWithoutRetrying(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		driver, delegated := pluginTenderOnStandby(t, w, map[string]bool{"resume": true, "resume_availability": true, "initial_prompt": true, "state_reporting": true})
		storage := serveResumeStorage(t, driver)
		onStandbyAndClosed(t, w, cli, delegated)

		storage.available.Store(false)
		reason := "the plugin's storage lost it"
		if sent := sendAgentMessage(t, cli, "sender", delegated.SeedID, "the pull request is approved"); !strings.Contains(sent.Detail, reason) {
			t.Fatalf("mail for a lost conversation = %+v, want it queued naming %q", sent, reason)
		}
		resumeBlockedNotice(t, app, cli, delegated.SeedID, reason)

		w.advance(5 * time.Minute)
		synctest.Wait()
		select {
		case launch := <-driver.launches:
			t.Fatalf("a blocked wake launched %+v", launch)
		default:
		}
		stillClosed(t, cli, delegated, "a blocked wake")
		if standby := lifeShow(t, cli, delegated.SeedID).Seed.Standby; standby == nil || !strings.Contains(protocol.Deref(standby.ResumeBlocked), reason) {
			t.Fatalf("the standby of %s is %+v, want resume_blocked naming %q", delegated.SeedID, standby, reason)
		}
	})
}

func TestAnAnswerWaitsForTheRetryOfATenderResumeThatFailedToLaunch(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setSetting(t, app, "asks_enabled", "true")
		driver, delegated := pluginTenderOnStandby(t, w, map[string]bool{"resume": true, "initial_prompt": true, "state_reporting": true})
		asked, err := cli.AskOpen(delegated.SessionID, "", cacheAsk, "", w.Path("tender"))
		if err != nil {
			t.Fatalf("ask: %v", err)
		}
		onStandbyAndClosed(t, w, cli, delegated)

		w.terms.RefuseNextSpawn(errors.New("PTY temporarily unavailable"))
		requestID := uuid.NewString()
		answered := testworld.Request(app, protocol.AskAnswerMessage{
			Cmd: protocol.CmdAskAnswer, RequestID: requestID, AskID: asked.ID,
			Answers: []protocol.AskAnswer{{Key: "q1", Picked: []string{"Postgres"}}},
		}, protocol.EventAskAnswerResult, func(r protocol.AskAnswerResultMessage) bool { return r.RequestID == requestID })
		if !answered.Success {
			t.Fatalf("answer: %s", protocol.Deref(answered.Error))
		}
		driver.launched()
		synctest.Wait()
		select {
		case launch := <-driver.launches:
			t.Fatalf("the failed wake retried at once with %+v, want it to wait for the retry", launch)
		default:
		}
		stillClosed(t, cli, delegated, "a failed launch")

		w.advance(5 * time.Minute)
		retried := driver.launched()
		if err := driver.state(retried, 1, "waiting_input"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if typed := strings.Join(w.terminal(string(delegated.SessionID)).Submitted(), "\n"); !strings.Contains(typed, standbyNote) || !strings.Contains(typed, "Postgres") {
			t.Fatalf("the woken tender received %q, want the standby note and the answer", typed)
		}
	})
}

func TestATenderResumeThatFailsOnItsLastAttemptIsBlockedUntilNewMailResumesIt(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		driver, delegated := pluginTenderOnStandby(t, w, map[string]bool{"resume": true, "initial_prompt": true, "state_reporting": true})
		onStandbyAndClosed(t, w, cli, delegated)

		reason := "PTY temporarily unavailable"
		w.terms.RefuseNextSpawn(errors.New(reason))
		sendAgentMessage(t, cli, "sender", delegated.SeedID, "the pull request is approved")
		driver.launched()
		synctest.Wait()
		noResumeBlocked(t, app, cli, delegated.SeedID)
		w.terms.RefuseNextSpawn(errors.New(reason))
		w.advance(5 * time.Minute)
		driver.launched()
		synctest.Wait()
		noResumeBlocked(t, app, cli, delegated.SeedID)
		w.terms.RefuseNextSpawn(errors.New(reason))
		w.advance(5 * time.Minute)
		driver.launched()
		synctest.Wait()
		stillClosed(t, cli, delegated, "the last failed attempt")
		resumeBlockedNotice(t, app, cli, delegated.SeedID, reason)
		if standby := lifeShow(t, cli, delegated.SeedID).Seed.Standby; standby == nil || !strings.Contains(protocol.Deref(standby.ResumeBlocked), reason) {
			t.Fatalf("the standby of %s is %+v, want resume_blocked naming %q", delegated.SeedID, standby, reason)
		}

		sendAgentMessage(t, cli, "sender", delegated.SeedID, "the checks passed too")
		w.advance(5 * time.Minute)
		retried := driver.launched()
		if err := driver.state(retried, 1, "waiting_input"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if standby := lifeShow(t, cli, delegated.SeedID).Seed.Standby; standby == nil || protocol.Deref(standby.ResumeBlocked) != "" {
			t.Fatalf("after new mail woke the tender the standby is %+v, want no blocked wake", standby)
		}
		if mail := seedMail(t, cli, string(delegated.SessionID), delegated.SeedID); len(mail) != 2 {
			t.Fatalf("the woken tender reads %+v, want both messages", mail)
		}
	})
}
