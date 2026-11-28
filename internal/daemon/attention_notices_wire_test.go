package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	stuckWaitAnswer  = `{"verdict":"OPEN","label":"stuck wait","reason":"a staging deploy ends within 10 min"}`
	quietWaitAnswer  = `{"verdict":"QUIET","reason":"a staging deploy can take this long"}`
	waitNoticeHeader = "attn supervisor · background wait check"
	noticeAnswerTime = 5 * time.Minute
)

type waitingAgent struct {
	t      *testing.T
	w      *world
	app    *testworld.Peer
	claude *bubbleClaude
	deploy protocol.BackgroundTask
	rings  int
}

func waitingOnADeploy(t *testing.T, w *world, supervisor bool, elapsedAnswer string) *waitingAgent {
	t.Helper()
	w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
		switch {
		case strings.Contains(task.Prompt, supervisorQuestion) && strings.Contains(task.Prompt, `"checkpoint": "elapsed_`):
			task.Answer(elapsedAnswer)
		case strings.Contains(task.Prompt, supervisorQuestion):
			task.Answer(quietWaitAnswer)
		case strings.Contains(task.Prompt, "Classify how this assistant message ends"):
			task.Answer(`{"verdict":"DONE"}`)
		default:
			task.Answer("Deploy work")
		}
	})
	app := w.TrustedApp()
	setSetting(t, app, "queue_mode_enabled", "true")
	if supervisor {
		setSetting(t, app, "attention_supervisor", "true")
	}
	claude := w.bubbleClaude(t, app, "shop")
	if booted := queriedSession(t, claude.cli, claude.id); protocol.Deref(booted.TurnOwed) {
		settleFromApp(app, booted)
	}
	agent := &waitingAgent{t: t, w: w, app: app, claude: claude, deploy: protocol.BackgroundTask{ID: protocol.Ptr("bdeploy"), Kind: protocol.Ptr("shell"),
		Description: protocol.Ptr("Deploy staging"), Command: protocol.Ptr("./deploy.sh staging"), Wakes: protocol.Ptr(protocol.BackgroundTaskWakeExit)}}
	claude.take("deploy staging")
	agent.stopsWaiting("Started the staging deploy in the background; I'll report when it ends.")
	if owed := agent.session(); protocol.Deref(owed.TurnOwed) {
		settleFromApp(app, owed)
	}
	return agent
}

func (a *waitingAgent) stopsWaiting(text string) {
	a.t.Helper()
	a.claude.cli = a.w.Client()
	synctest.Wait()
	a.claude.transcript.Answer(text + " <!-- attn:state=idle -->")
	if err := a.claude.cli.SendStop(protocol.TerminalID(a.claude.self), a.claude.transcript.Path, client.StopFacts{BackgroundTasks: []protocol.BackgroundTask{a.deploy}}); err != nil {
		a.t.Fatal(err)
	}
	a.w.advance(0)
}

func (a *waitingAgent) answers(text string) {
	a.t.Helper()
	rings := a.claude.promptsContaining(inboxDoorbell)
	if rings <= a.rings {
		a.t.Fatalf("the agent took the prompts %q, want a doorbell for the notice", a.claude.prompts())
	}
	a.rings = rings
	a.stopsWaiting(text)
}

func (a *waitingAgent) ignoresRings() {
	a.claude.term.OnSubmit(func(string) {})
}

func (a *waitingAgent) restart() {
	a.t.Helper()
	a.w.restart()
	a.claude.cli = a.w.Client()
	a.app = a.w.TrustedApp()
}

func (a *waitingAgent) notices() []string {
	a.t.Helper()
	return supervisorNotices(a.t, a.w.Client(), a.claude.id)
}

func (a *waitingAgent) noticeOnly(when, want string) string {
	a.t.Helper()
	notices := a.notices()
	if len(notices) != 1 || !strings.Contains(notices[0], want) {
		a.t.Fatalf("%s the agent got the notices %q, want one containing %q", when, notices, want)
	}
	return notices[0]
}

func (a *waitingAgent) noNotice(when string) {
	a.t.Helper()
	if notices := a.notices(); len(notices) != 0 {
		a.t.Fatalf("%s the agent got the notices %q, want none", when, notices)
	}
}

func (a *waitingAgent) session() protocol.Session {
	a.t.Helper()
	return queriedSession(a.t, a.w.Client(), a.claude.id)
}

func supervisorNotices(t *testing.T, cli *client.Client, session string) []string {
	t.Helper()
	var notices []string
	for _, item := range readInbox(t, cli, session, 0).Items {
		if protocol.Deref(item.SourceID) == "attention-supervisor" {
			notices = append(notices, item.Content)
		}
	}
	return notices
}

func daemonLogOf(t *testing.T, w *world) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(w.Dir, "daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTheTwentyMinuteCheckNoticesOnlyAWaitTheSupervisorCallsStuck(t *testing.T) {
	t.Run("stuck", func(t *testing.T) {
		inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
			agent := waitingOnADeploy(t, w, true, stuckWaitAnswer)
			w.advance(19 * time.Minute)
			agent.noNotice("19 minutes into the wait")
			w.advance(time.Minute)
			notice := agent.noticeOnly("20 minutes into the wait", `Your background task "Deploy staging" (bdeploy) has run 20 min`)
			if !strings.HasPrefix(notice, waitNoticeHeader) || !strings.Contains(notice, "Check whether it can still finish") {
				t.Errorf("the 20-minute notice reads %q, want the supervisor's check", notice)
			}
			if turn := agent.session(); protocol.Deref(turn.TurnOwed) {
				t.Errorf("the notice opened the agent's turn: %+v", turn)
			}
		})
	})
	t.Run("quiet", func(t *testing.T) {
		inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
			agent := waitingOnADeploy(t, w, true, quietWaitAnswer)
			w.advance(20 * time.Minute)
			agent.noNotice("20 minutes into a wait the supervisor calls quiet")
		})
	})
}

func TestTheSixtyMinuteAndFourHourChecksNoticeAWaitWhateverTheVerdict(t *testing.T) {
	inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
		agent := waitingOnADeploy(t, w, true, quietWaitAnswer)
		w.advance(time.Hour)
		agent.noticeOnly("60 minutes into a wait the supervisor calls quiet", `"Deploy staging" (bdeploy) has run 1 h`)
		w.advance(time.Minute)
		agent.answers("Checked the deploy log; it is on step 4 of 6 and ends by 1 h 30 min. Keeping the wait.")
		w.advance(3*time.Hour - 2*time.Minute)
		agent.noNotice("a minute before the 4-hour check")
		w.advance(time.Minute)
		notice := agent.noticeOnly("4 hours into the wait", "has run 4 h")
		if !strings.Contains(notice, "This is attn's last check of this wait.") {
			t.Errorf("the 4-hour notice reads %q, want it to say it is the last check", notice)
		}
	})
}

func TestAWaitGetsAtMostThreeNoticesAndKeepingOnAfterTheLastOpensTheTurn(t *testing.T) {
	inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
		agent := waitingOnADeploy(t, w, true, stuckWaitAnswer)
		w.advance(20 * time.Minute)
		agent.noticeOnly("at 20 minutes", "has run 20 min")
		w.advance(time.Minute)
		agent.answers("The deploy is on step 4 of 6. Keeping the wait.")
		w.advance(39 * time.Minute)
		agent.noticeOnly("at 60 minutes", "has run 1 h, and you have made no model request for 40 min.\n\nAt the last check you answered: \"The deploy is on step 4 of 6. Keeping the wait.\"")
		w.advance(time.Minute)
		agent.answers("Still on step 5 of 6. Keeping the wait.")
		w.advance(3*time.Hour - time.Minute)
		agent.noticeOnly("at 4 hours", "This is attn's last check of this wait.")
		if turn := agent.session(); protocol.Deref(turn.TurnOwed) {
			t.Fatalf("the agent's turn opened before it answered the last check: %+v", turn)
		}
		w.advance(time.Minute)
		agent.answers("Waiting on the staging deploy, which should end within the hour.")
		owed := agent.session()
		if !protocol.Deref(owed.TurnOwed) || owed.Attention == nil || owed.Attention.Decider != "rule:kept_waiting" || protocol.Deref(owed.Attention.Label) != "stuck wait" {
			t.Fatalf("after the agent kept waiting at the last check its session is %+v, want its turn open by rule:kept_waiting", owed)
		}
		w.advance(time.Hour)
		agent.noNotice("an hour after the last check")
	})
}

func TestANoticeWithNoModelRequestWithinFiveMinutesOpensTheTurn(t *testing.T) {
	inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
		agent := waitingOnADeploy(t, w, true, stuckWaitAnswer)
		agent.ignoresRings()
		w.advance(20 * time.Minute)
		agent.noticeOnly("at 20 minutes", "has run 20 min")
		w.advance(noticeAnswerTime - time.Second)
		if turn := agent.session(); protocol.Deref(turn.TurnOwed) {
			t.Fatalf("the turn opened before the agent's 5 minutes were up: %+v", turn)
		}
		w.advance(time.Second)
		owed := agent.session()
		if !protocol.Deref(owed.TurnOwed) || owed.Attention == nil || owed.Attention.Decider != "rule:notice_unanswered" ||
			!strings.Contains(owed.Attention.Reason, "the supervisor's 20 min check") {
			t.Errorf("5 minutes after an unanswered notice the session is %+v, want its turn open by rule:notice_unanswered", owed)
		}
	})
}

func TestNoNoticeGoesOutInTheFiveMinutesAfterADaemonStart(t *testing.T) {
	inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
		agent := waitingOnADeploy(t, w, true, quietWaitAnswer)
		w.advance(57 * time.Minute)
		agent.restart()
		w.advance(3 * time.Minute)
		agent.stopsWaiting("Still waiting on the staging deploy.")
		settleFromApp(agent.app, agent.session())
		agent.noNotice("at the 60-minute mark, 3 minutes after a restart")
		w.advance(2 * time.Minute)
		agent.noticeOnly("5 minutes after the restart", `"Deploy staging" (bdeploy) has run 1 h 2 min`)
	})
}

func TestAWaitNoticeAnsweredBeforeARestartStaysAnsweredAfterIt(t *testing.T) {
	inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
		agent := waitingOnADeploy(t, w, true, stuckWaitAnswer)
		w.advance(20 * time.Minute)
		agent.noticeOnly("at 20 minutes", "has run 20 min")
		w.advance(time.Minute)
		agent.answers("The deploy is on step 4 of 6. Keeping the wait.")
		w.advance(10 * time.Minute)
		agent.restart()
		w.advance(28 * time.Minute)
		agent.noNotice("a minute before the 60-minute check, after a restart")
		w.advance(time.Minute)
		agent.noticeOnly("at the 60-minute check after a restart", "has run 1 h, and you have made no model request for 40 min.\n\nAt the last check you answered: \"The deploy is on step 4 of 6. Keeping the wait.\"")
		if turn := agent.session(); protocol.Deref(turn.TurnOwed) {
			t.Errorf("the restart opened the turn of an agent that answered its notice: %+v", turn)
		}
	})
}

func TestWithTheSupervisorOffAWaitNoticeIsLoggedAndNotSent(t *testing.T) {
	inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
		agent := waitingOnADeploy(t, w, false, quietWaitAnswer)
		w.advance(time.Hour + noticeAnswerTime)
		agent.noNotice("with the supervisor off")
		if turn := agent.session(); protocol.Deref(turn.TurnOwed) {
			t.Errorf("with the supervisor off the turn opened: %+v", turn)
		}
		if log := daemonLogOf(t, w); !strings.Contains(log, "would send "+agent.claude.id+" this notice") || !strings.Contains(log, "(bdeploy) has run 1 h") {
			t.Errorf("with the supervisor off the daemon log holds no notice it would send; tail: %s", log[max(0, len(log)-2000):])
		}
	})
}

type delegateWorld struct {
	t        *testing.T
	w        *world
	app      *testworld.Peer
	cli      *client.Client
	delegate string
	stake    string
	agent    *fakeagent.Run
}

const openDelegateAnswer = `{"verdict":"OPEN","label":"unread report","reason":"the migration landed and only this session reports it"}`

func delegateStopsQuietly(t *testing.T, supervisor bool, role string, answer string) *delegateWorld {
	t.Helper()
	w := newTitlingWorld(t, fakeagent.Claude)
	w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
		switch {
		case strings.Contains(task.Prompt, supervisorQuestion):
			task.Answer(answer)
		case strings.Contains(task.Prompt, "Classify how this assistant message ends"):
			task.Answer(`{"verdict":"DONE"}`)
		default:
			task.Answer("Migration")
		}
	})
	app, cli := w.App(), w.Client()
	setSetting(t, app, "queue_mode_enabled", "true")
	if supervisor {
		setSetting(t, app, "attention_supervisor", "true")
	}
	registerSessions(t, w, cli, "reviewer")
	cwd := registerDelegationCaller(t, w, cli, "caller")
	message := delegateFrom("caller", cwd, "Migrate the first table", fakeagent.Claude)
	if role != "" {
		preferences := *loadDelegationPreferences(app).Preferences
		preferences.Enabled, preferences.WorkflowSkillEnabled, preferences.Roles = true, true, loadDelegationPreferences(app).Templates
		if saved := addAttnDelegationRoles(app, preferences); !saved.Success {
			t.Fatalf("adding attn's roles: %+v", saved)
		}
		message.Role = protocol.Ptr(role)
	}
	delegated, err := cli.Delegate(message)
	if err != nil {
		t.Fatal(err)
	}
	delegate := string(delegated.SessionID)
	agent := w.Launched(delegate)
	agent.Prompted()
	working := testworld.AwaitSession(app, delegate, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	stopped := runEnds(w, app, agent, working, "Migrated the first table.")
	if protocol.Deref(stopped.TurnOwed) {
		stopped = settleFromApp(app, stopped)
	}
	working, _ = notificationRunStarts(t, app, cli, agent, delegate, stopped, "the review is in")
	if quiet := runEnds(w, app, agent, working, "The review found one bug, so I fixed it and landed it."); protocol.Deref(quiet.TurnOwed) {
		t.Fatalf("the delegate's quiet stop opened a turn: %+v", quiet)
	}
	w.awaitTimer(daemon.AttentionStopTimer)
	return &delegateWorld{t: t, w: w, app: app, cli: cli, delegate: delegate, stake: delegated.SeedID, agent: agent}
}

func (d *delegateWorld) judged() {
	d.w.advance(attentionStopWait)
}

func (d *delegateWorld) turnOwed() bool {
	return protocol.Deref(queriedSession(d.t, d.cli, d.delegate).TurnOwed)
}

func TestTheSupervisorAsksALiveDispatcherBeforeItOpensItsDelegatesTurn(t *testing.T) {
	d := delegateStopsQuietly(t, true, "", openDelegateAnswer)
	d.judged()
	notices := supervisorNotices(t, d.cli, "caller")
	name := queriedSession(t, d.cli, d.delegate).Label
	says := "needs the user now. Its reason: the migration landed and only this session reports it"
	if len(notices) != 1 || !strings.Contains(notices[0], "your delegate "+name+" (") || !strings.Contains(notices[0], says) {
		t.Fatalf("the dispatcher got the notices %q, want one naming %q and saying it %s", notices, name, says)
	}
	if d.turnOwed() {
		t.Fatalf("the open verdict opened the delegate's turn before its dispatcher answered")
	}
	d.w.awaitTimer(daemon.AttentionNoticeTimer)
	d.w.advance(noticeAnswerTime)
	owed := testworld.AwaitSession(d.app, d.delegate, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) && s.Attention != nil })
	if owed.Attention.Decider != "rule:dispatcher_silent" || !strings.Contains(owed.Attention.Reason, "its dispatcher made no model request") {
		t.Errorf("5 minutes after the dispatcher's notice the delegate carries %+v, want its turn open by rule:dispatcher_silent", owed.Attention)
	}
}

func TestADispatcherThatActsOnTheNoticeLeavesTheDelegatesTurnClosed(t *testing.T) {
	t.Run("it closes the delegate", func(t *testing.T) {
		d := delegateStopsQuietly(t, true, "", openDelegateAnswer)
		d.judged()
		if notices := supervisorNotices(t, d.cli, "caller"); len(notices) != 1 {
			t.Fatalf("the dispatcher got the notices %q, want one", notices)
		}
		d.w.awaitTimer(daemon.AttentionNoticeTimer)
		if _, err := d.cli.AgentClose(d.delegate, "caller", "migration done"); err != nil {
			t.Fatal(err)
		}
		awaitClosed(d.app, d.delegate)
		d.w.advance(noticeAnswerTime)
		if n := surfacedTurns(t, d.w, d.delegate); n != 0 {
			t.Errorf("the closed delegate's turn was surfaced %d times", n)
		}
	})
	t.Run("it makes a model request", func(t *testing.T) {
		d := delegateStopsQuietly(t, true, "", openDelegateAnswer)
		d.judged()
		if notices := supervisorNotices(t, d.cli, "caller"); len(notices) != 1 {
			t.Fatalf("the dispatcher got the notices %q, want one", notices)
		}
		d.w.awaitTimer(daemon.AttentionNoticeTimer)
		d.w.advance(time.Minute)
		if err := d.cli.UpdateStateFromHookEvidence(protocol.TerminalID(d.w.Terminal("caller")), protocol.StateWorking, "", "user_prompt_submit", "the delegate's report is in hand; leaving it"); err != nil {
			t.Fatal(err)
		}
		d.w.advance(noticeAnswerTime - time.Minute)
		if d.turnOwed() {
			t.Errorf("the delegate's turn opened although its dispatcher answered the notice")
		}
	})
}

func TestADelegateWithNoLiveDispatcherGetsTheVerdictsActionAtOnce(t *testing.T) {
	for _, tc := range []struct {
		name   string
		role   string
		before func(*testing.T, *delegateWorld)
	}{
		{name: "its result goes to the user", role: "pathfinder"},
		{name: "its dispatcher is gone", before: func(t *testing.T, d *delegateWorld) { closeSession(t, d.cli, "caller", "done for the day") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := delegateStopsQuietly(t, true, tc.role, openDelegateAnswer)
			if tc.before != nil {
				tc.before(t, d)
			}
			d.judged()
			owed := testworld.AwaitSession(d.app, d.delegate, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) && s.Attention != nil })
			if owed.Attention.Decider != "headless/claude-haiku-fake" {
				t.Errorf("the delegate's turn carries %+v, want the verdict itself", owed.Attention)
			}
		})
	}
}

func TestADelegateWhoseTurnIsOwedSendsItsDispatcherNoNoticeWhileItWaits(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
		switch {
		case strings.Contains(task.Prompt, supervisorQuestion):
			task.Answer(stuckWaitAnswer)
		case strings.Contains(task.Prompt, "Classify how this assistant message ends"):
			task.Answer(`{"verdict":"DONE"}`)
		default:
			task.Answer("Migration")
		}
	})
	app, cli := w.App(), w.Client()
	setSetting(t, app, "queue_mode_enabled", "true")
	setSetting(t, app, "attention_supervisor", "true")
	cwd := registerDelegationCaller(t, w, cli, "caller")
	delegated, err := cli.Delegate(delegateFrom("caller", cwd, "Migrate the first table", fakeagent.Claude))
	if err != nil {
		t.Fatal(err)
	}
	delegate := string(delegated.SessionID)
	agent := w.Launched(delegate)
	agent.Prompted()
	working := testworld.AwaitSession(app, delegate, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	agent.LaunchBackgroundShell("bdeploy", "Deploy staging", "./deploy.sh staging")
	if owed := runEnds(w, app, agent, working, "Started the staging deploy; I'll report when it ends."); !protocol.Deref(owed.TurnOwed) {
		t.Fatalf("the delegate's stop left %+v, want its turn owed to the user", owed)
	}
	w.awaitTimer(daemon.AttentionElapsedTimer)
	w.advance(time.Hour + noticeAnswerTime)
	if notices := supervisorNotices(t, cli, "caller"); len(notices) != 0 {
		t.Errorf("an hour into the wait of a delegate the user already owes a turn, its dispatcher got the notices %q", notices)
	}
}

func TestWithTheSupervisorOffADispatcherNoticeIsLoggedAndNotSent(t *testing.T) {
	d := delegateStopsQuietly(t, false, "", openDelegateAnswer)
	d.judged()
	d.w.advance(noticeAnswerTime)
	if notices := supervisorNotices(t, d.cli, "caller"); len(notices) != 0 {
		t.Errorf("with the supervisor off the dispatcher got the notices %q", notices)
	}
	if d.turnOwed() {
		t.Errorf("with the supervisor off the delegate's turn opened")
	}
	if log := daemonLogOf(t, d.w); !strings.Contains(log, "would send caller this notice") || !strings.Contains(log, "needs the user now") {
		t.Errorf("with the supervisor off the daemon log holds no dispatcher notice; tail: %s", log[max(0, len(log)-2000):])
	}
}

type bubbleDelegation struct {
	t          *testing.T
	w          *world
	app        *testworld.Peer
	cli        *client.Client
	dispatcher *bubbleClaude
	delegate   *bubbleClaude
	stake      string
}

func delegateStopsQuietlyInBubble(t *testing.T, w *world) *bubbleDelegation {
	t.Helper()
	w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
		switch {
		case strings.Contains(task.Prompt, supervisorQuestion):
			task.Answer(openDelegateAnswer)
		case strings.Contains(task.Prompt, "Classify how this assistant message ends"):
			task.Answer(`{"verdict":"DONE"}`)
		default:
			task.Answer("Migration")
		}
	})
	app, cli := w.TrustedApp(), w.Client()
	setSetting(t, app, "queue_mode_enabled", "true")
	setSetting(t, app, "attention_supervisor", "true")
	registerSessions(t, w, cli, "reviewer")
	dispatcher := w.bubbleClaude(t, app, "caller")
	if booted := queriedSession(t, cli, dispatcher.id); protocol.Deref(booted.TurnOwed) {
		settleFromApp(app, booted)
	}
	dispatcher.take("delegate the table migration")
	request := delegateFrom(dispatcher.id, w.Path("worker"), "Migrate the first table", fakeagent.Claude)
	request.RequestID = "dispatcher-deadline"
	if err := os.MkdirAll(w.Path("worker"), 0o755); err != nil {
		t.Fatal(err)
	}
	accepted, err := cli.StartDelegation(request)
	if err != nil {
		t.Fatal(err)
	}
	w.advance(0)
	delegate := w.bootBubbleClaude(t, string(accepted.SessionID))
	delegate.reply("Migrated the first table. <!-- attn:state=idle -->")
	delegated, err := cli.Delegate(request)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.reply("Delegated the migration. <!-- attn:state=idle -->")
	for _, id := range []string{dispatcher.id, delegate.id} {
		if stopped := queriedSession(t, cli, id); protocol.Deref(stopped.TurnOwed) {
			settleFromApp(app, stopped)
		}
	}
	sendAgentMessage(t, cli, "reviewer", delegate.id, "the review is in")
	w.advance(sessionmonitor.QuietWindow)
	pasted := delegate.term.Pasted()
	if len(pasted) != 1 {
		t.Fatalf("attn pasted %q into the delegate, want one doorbell", pasted)
	}
	delegate.take(pasted[0])
	readInbox(t, cli, delegate.id, 0)
	delegate.reply("The review found one bug, so I fixed it and landed it. <!-- attn:state=idle -->")
	if quiet := queriedSession(t, cli, delegate.id); protocol.Deref(quiet.TurnOwed) {
		t.Fatalf("the delegate's quiet stop opened a turn: %+v", quiet)
	}
	dispatcher.term.OnSubmit(func(string) {})
	return &bubbleDelegation{t: t, w: w, app: app, cli: cli, dispatcher: dispatcher, delegate: delegate, stake: delegated.SeedID}
}

func (d *bubbleDelegation) noticesTheDispatcher() {
	d.t.Helper()
	d.w.advance(attentionStopWait)
	if notices := supervisorNotices(d.t, d.cli, d.dispatcher.id); len(notices) != 1 || !strings.Contains(notices[0], "needs the user now") {
		d.t.Fatalf("15 minutes after the delegate's quiet stop its dispatcher got the notices %q, want one", notices)
	}
	if d.turnOwed() {
		d.t.Fatalf("the delegate's turn opened before its dispatcher's 5 minutes ran out")
	}
}

func (d *bubbleDelegation) restart() {
	d.t.Helper()
	d.w.restart()
	d.reconnect()
}

func (d *bubbleDelegation) reconnect() {
	d.app, d.cli = d.w.TrustedApp(), d.w.Client()
	d.dispatcher.cli, d.delegate.cli = d.cli, d.cli
}

func (d *bubbleDelegation) dispatcherMakesAModelRequest() {
	d.t.Helper()
	d.dispatcher.take("the delegate's report is in hand; leaving it")
	synctest.Wait()
}

func (d *bubbleDelegation) turnOwed() bool {
	d.t.Helper()
	return protocol.Deref(queriedSession(d.t, d.cli, d.delegate.id).TurnOwed)
}

func (d *bubbleDelegation) turnOpenedByTheDispatchersSilence(when string) {
	d.t.Helper()
	owed := queriedSession(d.t, d.cli, d.delegate.id)
	if !protocol.Deref(owed.TurnOwed) || owed.Attention == nil || owed.Attention.Decider != "rule:dispatcher_silent" {
		d.t.Errorf("%s the delegate is %+v with attention %+v, want its turn open by rule:dispatcher_silent", when, owed, owed.Attention)
	}
}

func TestADispatchersSilenceAcrossARestartOpensTheDelegatesTurnOnceTheStartHoldEnds(t *testing.T) {
	for _, tc := range []struct {
		name string
		down func(*bubbleDelegation)
	}{
		{name: "it restarts inside the window", down: func(d *bubbleDelegation) { d.restart() }},
		{name: "it is down past the deadline", down: func(d *bubbleDelegation) {
			d.w.stop()
			d.w.advance(10 * time.Minute)
			d.w.start()
			d.reconnect()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
				d := delegateStopsQuietlyInBubble(t, w)
				d.noticesTheDispatcher()
				w.advance(2 * time.Minute)
				tc.down(d)
				w.advance(noticeAnswerTime - time.Second)
				if d.turnOwed() {
					t.Fatalf("a second before the start hold ended the delegate's turn is open")
				}
				w.advance(time.Second)
				d.turnOpenedByTheDispatchersSilence("5 minutes after a start, past the dispatcher's deadline,")
			})
		})
	}
}

func TestADispatcherThatAnswersInsideTheStartHoldLeavesTheDelegatesTurnClosed(t *testing.T) {
	inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
		d := delegateStopsQuietlyInBubble(t, w)
		d.noticesTheDispatcher()
		w.advance(2 * time.Minute)
		w.stop()
		w.advance(10 * time.Minute)
		w.start()
		d.reconnect()
		w.advance(2 * time.Minute)
		d.dispatcherMakesAModelRequest()
		w.advance(noticeAnswerTime)
		if d.turnOwed() {
			t.Errorf("the delegate's turn opened although its dispatcher answered inside the start hold, after its deadline passed while the daemon was down")
		}
	})
}

func TestADispatcherThatClosesInsideItsWindowLeavesTheDelegatesTurnToItsSilence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		restart bool
		opensIn time.Duration
	}{
		{name: "without a restart", opensIn: noticeAnswerTime - time.Minute},
		{name: "across a restart", restart: true, opensIn: noticeAnswerTime},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
				d := delegateStopsQuietlyInBubble(t, w)
				d.noticesTheDispatcher()
				w.advance(time.Minute)
				closeSession(t, d.cli, d.dispatcher.id, "done for the day")
				awaitClosed(d.app, d.dispatcher.id)
				if tc.restart {
					d.restart()
				}
				w.advance(tc.opensIn - time.Second)
				if d.turnOwed() {
					t.Fatalf("a second before its deadline the delegate's turn is open")
				}
				w.advance(time.Second)
				d.turnOpenedByTheDispatchersSilence("at the deadline of a dispatcher that closed inside its window")
			})
		})
	}
}

func TestADispatcherThatAnswersAroundARestartLeavesTheDelegatesTurnClosed(t *testing.T) {
	t.Run("it answers before the restart", func(t *testing.T) {
		inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
			d := delegateStopsQuietlyInBubble(t, w)
			d.noticesTheDispatcher()
			w.advance(time.Minute)
			d.dispatcherMakesAModelRequest()
			w.advance(time.Minute)
			d.restart()
			w.advance(noticeAnswerTime)
			if d.turnOwed() {
				t.Errorf("the delegate's turn opened after a restart although its dispatcher answered the notice before it")
			}
		})
	})
	t.Run("it answers after the restart", func(t *testing.T) {
		inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
			d := delegateStopsQuietlyInBubble(t, w)
			d.noticesTheDispatcher()
			w.advance(2 * time.Minute)
			d.restart()
			w.advance(time.Minute)
			d.dispatcherMakesAModelRequest()
			w.advance(noticeAnswerTime)
			if d.turnOwed() {
				t.Errorf("the delegate's turn opened although its dispatcher answered the notice after a restart")
			}
		})
	})
}

func TestADelegateClosedInItsDispatchersWindowStaysClosedAcrossARestart(t *testing.T) {
	inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
		d := delegateStopsQuietlyInBubble(t, w)
		d.noticesTheDispatcher()
		w.advance(time.Minute)
		if _, err := d.cli.AgentClose(d.delegate.id, protocol.SessionID(d.dispatcher.id), "migration done"); err != nil {
			t.Fatal(err)
		}
		awaitClosed(d.app, d.delegate.id)
		d.restart()
		w.advance(noticeAnswerTime)
		if n := surfacedTurns(t, w, d.delegate.id); n != 0 {
			t.Errorf("the delegate closed in its dispatcher's window had its turn surfaced %d times across a restart", n)
		}
	})
}

func TestADelegatesTurnOpenedByItsDispatchersSilenceAndSettledStaysSettledAcrossARestart(t *testing.T) {
	inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
		d := delegateStopsQuietlyInBubble(t, w)
		d.noticesTheDispatcher()
		w.advance(noticeAnswerTime)
		d.turnOpenedByTheDispatchersSilence("5 minutes after the notice")
		settleFromApp(d.app, queriedSession(t, d.cli, d.delegate.id))
		w.advance(time.Minute)
		d.restart()
		w.advance(noticeAnswerTime)
		if d.turnOwed() {
			t.Errorf("a restart reopened the delegate's turn that its dispatcher's silence opened and the user settled")
		}
	})
}
