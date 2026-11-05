package daemon_test

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
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
	got := relaunched.Prompted()
	if !strings.Contains(got, standbyNote) || !strings.Contains(got, seed) {
		t.Fatalf("the woken tender received %q, want the standby note of %s", got, seed)
	}
	awaitStandbyEnded(t, app, mark, seed)
	if mail := seedMail(t, cli, string(session), seed); len(mail) != 1 || mail[0].Content != "the pull request is approved" {
		t.Fatalf("the woken tender's seed mailbox holds %+v, want the message", mail)
	}
	if reopened := showSession(t, cli, string(session)); protocol.Deref(reopened.ClosedAt) != "" {
		t.Errorf("the ledger still shows %s closed after the wake", session)
	}
}

func TestAWokenCodexTenderGetsItsMailOnlyOnceItHasBooted(t *testing.T) {
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
	if got := relaunched.Prompted(); !strings.Contains(got, standbyNote) {
		t.Fatalf("the woken Codex took %q, want the standby note once it booted", got)
	}
	awaitStandbyEnded(t, app, mark, seed)
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
	if got := relaunched.Prompted(); !strings.Contains(got, standbyNote) {
		t.Fatalf("the tender the user closed received %q, want the standby note", got)
	}
	awaitStandbyEnded(t, app, mark, seed)
	stillClosed(t, cli, idle, "mail for a seed that is not on standby")
	stillClosed(t, cli, direct, "a session: message to a tender whose seed is on standby")
}

func TestSeedMailReachesAWokenClaudeTenderThroughItsModAfterTheFirstPoll(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, claudeModSetting, "true")
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
	awaitStandbyEnded(t, app, mark, seed)
	nothingElsePrompted(t, app, relaunched, string(session))
}

func TestAWokenClaudeTenderWhoseModMissesItsFirstPollGetsNoTypedMail(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, claudeModSetting, "true")
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
	w.clock.Fire(daemon.ClaudeModFirstPollTimer)
	reason := "never polled"
	wakeBlockedNotice(t, app, cli, seed, reason)
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
	awaitStandbyEnded(t, app, mark, seed)
	nothingElsePrompted(t, app, relaunched, string(session))
}

func TestAClaudeTenderWithoutTheModCannotGoOnStandby(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	cli := w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	w.Launched(string(delegated.SessionID)).Prompted()

	_, err := standbyAs(t, cli, delegated, true)
	if err == nil || !strings.Contains(err.Error(), claudeModSetting) {
		t.Fatalf("seed standby from a Claude session without the mod = %v, want a refusal naming %s", err, claudeModSetting)
	}
	if shown := lifeShow(t, cli, delegated.SeedID); shown.Seed.Standby != nil {
		t.Errorf("the refused standby left %+v on the seed", shown.Seed.Standby)
	}
	if open := showSession(t, cli, string(delegated.SessionID)); protocol.Deref(open.ClosedAt) != "" {
		t.Errorf("the refused standby closed %s", delegated.SessionID)
	}
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

func TestTheTenderWakeSettingDecidesWhichSeedMailWakesATenderOnStandby(t *testing.T) {
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
		if got := run.Prompted(); !strings.Contains(got, standbyNote) {
			t.Fatalf("the tender woken by %s received %q, want its standby note", by, got)
		}
		return seedMail(t, cli, string(delegated.SessionID), delegated.SeedID)
	}
	pullRequestNotice := func(m protocol.AgentInboxItem) bool {
		return strings.Contains(m.Content, "Pull request monitor: ready")
	}

	setSetting(t, app, "garden.tender_wake", "watches")
	if sent := sendAgentMessage(t, cli, "sender", peerUnderWatches.SeedID, "a peer message"); !strings.Contains(sent.Detail, "garden.tender_wake=watches") {
		t.Fatalf("a peer message under watches = %+v, want it queued naming the setting", sent)
	}
	stillClosed(t, cli, peerUnderWatches, "a peer message under garden.tender_wake=watches")

	setSetting(t, app, "garden.tender_wake", "off")
	readyUnderOff := pullRequestReadyFor(pullRequestUnderOff)
	w.tickSchedulesUntil("the first reminder fires and the pull request update arrives", func() bool {
		return lifeShow(t, cli, underOff.SeedID).Seed.Reminder == nil && readyUnderOff()
	})
	for _, delegated := range []*protocol.DelegateResult{underOff, pullRequestUnderOff} {
		if sent := sendAgentMessage(t, cli, "sender", delegated.SeedID, "a peer message"); !strings.Contains(sent.Detail, "garden.tender_wake=off") {
			t.Fatalf("a peer message to %s after its notice under off = %+v, want it queued naming the setting", delegated.SeedID, sent)
		}
		stillClosed(t, cli, delegated, "a notice and a peer message under garden.tender_wake=off")
	}

	setSetting(t, app, "garden.tender_wake", "watches")
	w.tickSchedulesUntil("the second reminder fires", func() bool { return lifeShow(t, cli, reminderUnderWatches.SeedID).Seed.Reminder == nil })
	if mail := woken(reminderUnderWatches, "the reminder under watches"); len(mail) != 1 || protocol.Deref(mail[0].SourceID) != "seed-reminder" {
		t.Fatalf("the tender woken under watches holds %+v, want its reminder", mail)
	}
	w.tickSchedulesUntil("the pull request update arrives under watches", pullRequestReadyFor(pullRequestUnderWatches))
	if mail := woken(pullRequestUnderWatches, "the pull request update under watches"); len(mail) != 1 || !pullRequestNotice(mail[0]) {
		t.Fatalf("the tender woken under watches holds %+v, want the pull request update", mail)
	}

	setSetting(t, app, "garden.tender_wake", "any")
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

func wakeBlockedNotice(t *testing.T, app *testworld.Peer, cli *client.Client, seed, reason string) {
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

func TestTheFortyNinthTenderWakeInADayIsRefusedLoudlyAndTheMailWaitsForTheWindow(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.App(), w.Client()
		setSetting(t, app, "crew.heartbeat_enabled", "false")
		setSetting(t, app, "crew.autosleep_enabled", "false")
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
		if refused.TargetSessionID != "" || !strings.Contains(refused.Detail, "tender_wake_tripwire") {
			t.Fatalf("the 49th wake within a day = %+v, want it refused by the tripwire", refused)
		}
		wakeBlockedNotice(t, app, cli, seed, "tender_wake_tripwire")
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

func TestSeedMailWakesACrewMemberTenderOnStandbyWithItsNote(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.App(), w.Client()
		setSetting(t, app, "crew.heartbeat_enabled", "false")
		setSetting(t, app, "crew.autosleep_enabled", "false")
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
		if standby := lifeShow(t, cli, seed).Seed.Standby; standby != nil {
			t.Errorf("after the woken member got its mail %s still shows standby %+v", seed, standby)
		}
		if mail := seedMail(t, cli, day.id, seed); len(mail) != 1 || mail[0].Content != "the pull request is approved" {
			t.Fatalf("the woken member's seed mailbox holds %+v, want the message", mail)
		}
	})
}

func TestATenderWakeRefusedForARemovedWorktreeNotesAndNotifiesTheReason(t *testing.T) {
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
	if sent := sendAgentMessage(t, cli, "sender", delegated.SeedID, "the pull request is approved"); !strings.Contains(sent.Detail, reason) {
		t.Fatalf("mail for a tender whose worktree is gone = %+v, want it queued naming %q", sent, reason)
	}
	stillClosed(t, cli, delegated, "mail for a removed worktree")
	wakeBlockedNotice(t, app, cli, delegated.SeedID, reason)
	if standby := lifeShow(t, cli, delegated.SeedID).Seed.Standby; standby == nil || !strings.Contains(protocol.Deref(standby.WakeBlocked), reason) {
		t.Errorf("the seed's standby is %+v, want wake_blocked naming %q", standby, reason)
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
