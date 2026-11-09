package daemon_test

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type runRecord struct {
	StartedBy     string `json:"started_by"`
	DurationMs    *int64 `json:"duration_ms"`
	Ended         string `json:"ended"`
	OpenedTurn    bool   `json:"opened_turn"`
	QuietStop     bool   `json:"quiet_stop"`
	TurnRequested bool   `json:"turn_requested"`
	TurnAfterStop string `json:"turn_after_stop"`
	UserInputMs   *int64 `json:"user_input_ms"`
	Approvals     int    `json:"approvals"`
	Usage         string `json:"usage"`
	Tokens        *struct {
		Input      int `json:"input"`
		Output     int `json:"output"`
		CacheRead  int `json:"cache_read"`
		CacheWrite int `json:"cache_write"`
	} `json:"tokens"`
	Session analytics.SessionTraits `json:"session"`
}

func (r runRecord) String() string {
	tokens := "none"
	if r.Tokens != nil {
		tokens = fmt.Sprint(r.Tokens.Input + r.Tokens.Output + r.Tokens.CacheRead + r.Tokens.CacheWrite)
	}
	return fmt.Sprintf("started_by=%s ended=%s opened_turn=%v approvals=%d usage=%s tokens=%s harness=%s",
		r.StartedBy, r.Ended, r.OpenedTurn, r.Approvals, r.Usage, tokens, r.Session.Harness)
}

func runRecords(t *testing.T, w *world, app *testworld.Peer, session string) []runRecord {
	t.Helper()
	secret, err := hex.DecodeString(strings.TrimSpace(readSecret(t, filepath.Join(w.Dir, "analytics-secret"))))
	if err != nil {
		t.Fatal(err)
	}
	subject := analytics.HashSubject(secret, session)
	var runs []runRecord
	for _, record := range streamOutbox(t, app, analytics.StreamRun) {
		if record.Name != "run" || record.Subject != subject {
			continue
		}
		var run runRecord
		if err := json.Unmarshal(record.Props, &run); err != nil {
			t.Fatalf("run record props %s: %v", record.Props, err)
		}
		if run.DurationMs == nil || *run.DurationMs < 0 {
			t.Errorf("run record %s has no duration", record.Props)
		}
		runs = append(runs, run)
	}
	slices.Reverse(runs)
	return runs
}

func runEnds(w *world, app *testworld.Peer, agent *fakeagent.Run, working protocol.Session, reply string) protocol.Session {
	w.T.Helper()
	agent.Reply(reply)
	stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
	w.readTranscripts()
	return stopped
}

func assertRuns(t *testing.T, got []runRecord, want []string) {
	t.Helper()
	lines := make([]string, len(got))
	for i, run := range got {
		lines[i] = run.String()
	}
	if !slices.Equal(lines, want) {
		t.Errorf("run records:\n  %s\nwant:\n  %s", strings.Join(lines, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestEachClaudeRunIsOneRunRecordNamingWhatStartedIt(t *testing.T) {
	w, app, cli := quietNotificationsWorld(t, true)
	setAnalyticsEnabled(app, "true")
	launchReply := "Planned the refund flow. <!-- attn:state=idle -->"
	session, agent, working := mailAgentAtWork(w, app, "shop", "plan the refund flow")
	stopped := runEnds(w, app, agent, working, launchReply)

	settled := settleFromApp(app, stopped)
	working, _ = notificationRunStarts(t, app, cli, agent, session, settled, "the review is in")
	ringReply := "Nothing to do. <!-- attn:state=idle -->"
	stopped = runEnds(w, app, agent, working, ringReply)

	app.TypeLine(session, "add a refund button")
	agent.Prompted()
	working = testworld.AwaitStateAfter(app, stopped, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	userReply := "Added it. Ship it? <!-- attn:state=waiting_input -->"
	stopped = runEnds(w, app, agent, working, userReply)

	agent.ProgramStatus("state=working:app=claude-code")
	working = testworld.AwaitStateAfter(app, stopped, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	resumedReply := "The background build passed. <!-- attn:state=idle -->"
	runEnds(w, app, agent, working, resumedReply)

	assertRuns(t, runRecords(t, w, app, session), []string{
		fmt.Sprintf("started_by=launch ended=idle opened_turn=true approvals=0 usage=known tokens=%d harness=claude", claudeTokens(launchReply)),
		fmt.Sprintf("started_by=ring ended=idle opened_turn=false approvals=0 usage=known tokens=%d harness=claude", claudeTokens(ringReply)),
		fmt.Sprintf("started_by=user ended=waiting_input opened_turn=true approvals=0 usage=known tokens=%d harness=claude", claudeTokens(userReply)),
		fmt.Sprintf("started_by=agent ended=idle opened_turn=false approvals=0 usage=known tokens=%d harness=claude", claudeTokens(resumedReply)),
	})
}

func TestACodexRunThroughAnApprovalIsOneRunRecord(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "auto_approve_enabled", "false")
	setAnalyticsEnabled(app, "true")
	session, codex, working := codexAtWork(t, w, app)
	before := queriedSession(t, w.Client(), session).Usage
	owedBefore := protocol.Deref(working.TurnOwed)

	codex.AskApproval()
	asking := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
	codex.Dismiss()
	working = testworld.AwaitStateAfter(app, asking, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	stopped := runEnds(w, app, codex, working, "Ran the migration. <!-- attn:state=idle -->")
	opened := !owedBefore && protocol.Deref(stopped.TurnOwed)

	after := queriedSession(t, w.Client(), session).Usage
	spent := after.TotalTokens
	if before != nil {
		spent -= before.TotalTokens
	}
	assertRuns(t, runRecords(t, w, app, session), []string{
		fmt.Sprintf("started_by=user ended=idle opened_turn=%v approvals=1 usage=known tokens=%d harness=codex", opened, spent),
	})
}

func TestARunWithNoWatchedTranscriptRecordsItsUsageUnavailable(t *testing.T) {
	t.Setenv("ATTN_AGENT_CLAUDE_TRANSCRIPT_WATCHER", "0")
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setAnalyticsEnabled(app, "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(session)
	idle := settleFromApp(app, testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle }))
	app.TypeLine(session, "find the flaky test")
	agent.Prompted()
	working := testworld.AwaitStateAfter(app, idle, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	agent.ReplyAfterStop("It races the tax lookup. <!-- attn:state=idle -->")
	stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(stopped.TurnOwed) {
		t.Fatal("the user's run stopped without opening a turn")
	}

	assertRuns(t, runRecords(t, w, app, session), []string{
		"started_by=user ended=idle opened_turn=true approvals=0 usage=unavailable tokens=none harness=claude",
	})
}

func claudeAtIdle(w *world, app *testworld.Peer) (string, *fakeagent.Run, protocol.Session) {
	w.T.Helper()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(session)
	idle := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	return session, agent, settleFromApp(app, idle)
}

func userRunStarts(app *testworld.Peer, agent *fakeagent.Run, session string, after protocol.Session, prompt string) protocol.Session {
	app.T.Helper()
	after = settleFromApp(app, after)
	app.TypeLine(session, prompt)
	agent.Prompted()
	return testworld.AwaitStateAfter(app, after, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
}

func TestARunRecordCoversOnlyRunsAnalyticsCapturedThroughout(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session, agent, idle := claudeAtIdle(w, app)

	working := userRunStarts(app, agent, session, idle, "find the flaky test")
	setAnalyticsEnabled(app, "true")
	stopped := runEnds(w, app, agent, working, "It races the tax lookup. <!-- attn:state=idle -->")

	working = userRunStarts(app, agent, session, stopped, "fix it")
	setAnalyticsEnabled(app, "false")
	setAnalyticsEnabled(app, "true")
	stopped = runEnds(w, app, agent, working, "Fixed the race. <!-- attn:state=idle -->")

	working = userRunStarts(app, agent, session, stopped, "add a regression test")
	capturedReply := "Added the test. <!-- attn:state=idle -->"
	runEnds(w, app, agent, working, capturedReply)

	assertRuns(t, runRecords(t, w, app, session), []string{
		fmt.Sprintf("started_by=user ended=idle opened_turn=true approvals=0 usage=known tokens=%d harness=claude", claudeTokens(capturedReply)),
	})
}

func TestARunStartedBeforeThePreviousRunWasReadCountsOnlyItsOwnTokens(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session, agent, idle := claudeAtIdle(w, app)
	working := userRunStarts(app, agent, session, idle, "read the failing build")
	stopped := runEnds(w, app, agent, working, "The tax tests fail. <!-- attn:state=idle -->")

	working = userRunStarts(app, agent, session, stopped, "find the flaky test")
	w.OnTranscriptWrite(func() {})
	agent.Reply("It races the tax lookup. <!-- attn:state=idle -->")
	stopped = testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
	setAnalyticsEnabled(app, "true")

	working = userRunStarts(app, agent, session, stopped, "fix it")
	w.readTranscripts()
	w.OnTranscriptWrite(w.readTranscripts)
	reply := "Fixed the race. <!-- attn:state=idle -->"
	runEnds(w, app, agent, working, reply)

	assertRuns(t, runRecords(t, w, app, session), []string{
		fmt.Sprintf("started_by=user ended=idle opened_turn=true approvals=0 usage=known tokens=%d harness=claude", claudeTokens(reply)),
	})
}

func TestARunAfterARecoveredRunCountsOnlyItsOwnTokens(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		agent := w.bubbleClaude(t, app, "shop")
		app.TypeLine(agent.id, "read the failing build")
		synctest.Wait()
		agent.reply("The tax tests fail. <!-- attn:state=idle -->")
		w.advance(500 * time.Millisecond)

		app.TypeLine(agent.id, "find the flaky test")
		agent.term.Heartbeat("busy", "Claude Code")
		synctest.Wait()
		w.restart()
		app = w.App()
		agent.cli = w.Client()
		w.advance(500 * time.Millisecond)
		agent.reply("It races the tax lookup. <!-- attn:state=idle -->")
		agent.term.Heartbeat("not_busy", "Claude Code")
		synctest.Wait()
		if got := queriedSession(t, agent.cli, agent.id).State; got != protocol.SessionStateIdle {
			t.Fatalf("the recovered run ended in %s, want idle", got)
		}

		setAnalyticsEnabled(app, "true")
		app.TypeLine(agent.id, "fix it")
		synctest.Wait()
		w.advance(500 * time.Millisecond)
		reply := "Fixed the race. <!-- attn:state=idle -->"
		agent.reply(reply)
		agent.term.Heartbeat("not_busy", "Claude Code")
		synctest.Wait()
		w.advance(500 * time.Millisecond)

		assertRuns(t, runRecords(t, w, app, agent.id), []string{
			fmt.Sprintf("started_by=user ended=idle opened_turn=false approvals=0 usage=known tokens=%d harness=claude", claudeTokens(reply)),
		})
	})
}

func TestAUserSteeringARunTheAgentStartedKeepsItsStartCause(t *testing.T) {
	steers := map[string]func(app *testworld.Peer, session string){
		"typed": func(app *testworld.Peer, session string) { app.TypeLine(session, "also run the linter") },
		"annotation": func(app *testworld.Peer, session string) {
			requestID := uuid.NewString()
			answer := testworld.Request(app, protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: requestID, SessionID: protocol.SessionID(session), Text: "also run the linter"},
				protocol.EventSessionAnnotationsSubmitResult, func(m protocol.SessionAnnotationsSubmitResultMessage) bool { return m.RequestID == requestID })
			if !answer.Success {
				app.T.Fatalf("annotation steer refused: %s %s", answer.Status, protocol.Deref(answer.Error))
			}
		},
	}
	for name, steer := range steers {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app := w.App()
			setAnalyticsEnabled(app, "true")
			session, agent, idle := claudeAtIdle(w, app)
			app.TypeLine(session, "")
			screenSnapshot(app, session)

			agent.ProgramStatus("state=working:app=claude-code")
			working := testworld.AwaitStateAfter(app, idle, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			steer(app, session)
			if name == "annotation" {
				modEvent(t, agent, fakeagent.ModEventHeld, "also run the linter")
				agent.ModToolBoundary()
			} else {
				agent.Prompted()
			}
			reply := "The build and the linter pass. <!-- attn:state=idle -->"
			runEnds(w, app, agent, working, reply)

			assertRuns(t, runRecords(t, w, app, session), []string{
				fmt.Sprintf("started_by=agent ended=idle opened_turn=true approvals=0 usage=known tokens=%d harness=claude", claudeTokens(reply)),
			})
		})
	}
}

func modAboutToBeAnswered(t *testing.T, w *world, app *testworld.Peer) (string, *fakeagent.Run, *modSocket, <-chan modPollAnswer, protocol.Session) {
	t.Helper()
	session, agent := moddedClaude(t, w, app)
	idle := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	socket := modSocketOf(t, agent)
	taken := socket.poll("late-custody")
	if event := agent.ModNext(); event.Kind != fakeagent.ModEventReplaced {
		t.Fatalf("the agent's own mod reported %+v, want replaced", event)
	}
	return session, agent, socket, taken, idle
}

func oneRunStartedBy(t *testing.T, w *world, app *testworld.Peer, session, want string) {
	t.Helper()
	runs := runRecords(t, w, app, session)
	if len(runs) != 1 || runs[0].StartedBy != want {
		t.Fatalf("run records %v, want one with started_by=%s", runs, want)
	}
}

func TestInputThatStartsARunThroughTheModNamesItWhenCustodyComesLast(t *testing.T) {
	inputs := map[string]struct {
		send func(t *testing.T, app *testworld.Peer, cli *client.Client, session string)
		want string
	}{
		"ring": {
			send: func(t *testing.T, _ *testworld.Peer, cli *client.Client, session string) {
				sendAgentMessage(t, cli, "reviewer", session, "the review is in")
			},
			want: "ring",
		},
		"annotation": {
			send: func(t *testing.T, app *testworld.Peer, _ *client.Client, session string) {
				requestID := uuid.NewString()
				answer := testworld.Request(app, protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: requestID, SessionID: protocol.SessionID(session), Text: "also run the linter"},
					protocol.EventSessionAnnotationsSubmitResult, func(m protocol.SessionAnnotationsSubmitResultMessage) bool { return m.RequestID == requestID })
				if !answer.Success {
					t.Errorf("annotation refused: %s %s", answer.Status, protocol.Deref(answer.Error))
				}
			},
			want: "user",
		},
	}
	for name, input := range inputs {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app, cli := w.App(), w.Client()
			setAnalyticsEnabled(app, "true")
			registerSessions(t, w, cli, "reviewer")
			session, agent, mod, taken, idle := modAboutToBeAnswered(t, w, app)

			sent := make(chan struct{})
			go func() {
				defer close(sent)
				input.send(t, app, cli, session)
			}()
			handed := <-taken
			agent.Input(handed.Text)
			agent.Prompted()
			working := testworld.AwaitStateAfter(app, idle, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			mod.ackSubmitted("late-custody", handed.ID)
			<-sent
			runEnds(w, app, agent, working, "Read it. <!-- attn:state=idle -->")

			oneRunStartedBy(t, w, app, session, input.want)
		})
	}
}

func TestARingTheModHeldOrRefusedLeavesARunThatStartedMeanwhileTheAgents(t *testing.T) {
	for _, outcome := range []string{"held", "refused"} {
		t.Run(outcome, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app, cli := w.App(), w.Client()
			setAnalyticsEnabled(app, "true")
			registerSessions(t, w, cli, "reviewer")
			session, agent, mod, taken, idle := modAboutToBeAnswered(t, w, app)

			sent := make(chan struct{})
			go func() {
				defer close(sent)
				sendAgentMessage(t, cli, "reviewer", session, "the review is in")
			}()
			handed := <-taken
			agent.ProgramStatus("state=working:app=claude-code")
			working := testworld.AwaitStateAfter(app, idle, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			mod.ack("late-custody", handed.ID, outcome)
			<-sent
			runEnds(w, app, agent, working, "The background build passed. <!-- attn:state=idle -->")

			oneRunStartedBy(t, w, app, session, "agent")
		})
	}
}

func TestARingTakenWhileAUsersRunStartsLeavesThatRunTheUsers(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setAnalyticsEnabled(app, "true")
	registerSessions(t, w, cli, "reviewer")
	session, agent, mod, taken, idle := modAboutToBeAnswered(t, w, app)

	sent := make(chan struct{})
	go func() {
		defer close(sent)
		sendAgentMessage(t, cli, "reviewer", session, "the review is in")
	}()
	handed := <-taken
	app.TypeLine(session, "add a refund button")
	agent.Prompted()
	working := testworld.AwaitStateAfter(app, idle, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	mod.ackSubmitted("late-custody", handed.ID)
	<-sent
	runEnds(w, app, agent, working, "Added it. <!-- attn:state=idle -->")

	oneRunStartedBy(t, w, app, session, "user")
}

func TestACrewHeartbeatThroughTheModRecordsAHeartbeatRun(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	writeCrewCharter(t, w, "trellis")
	w.restart()
	app, cli := w.App(), w.Client()
	setAnalyticsEnabled(app, "true")
	session := string(wakeCrew(t, cli, "trellis", "").SessionID)
	agent := w.Launched(session)
	w.awaitTimer(daemon.ClaudeModPollHoldTimer)
	agent.Prompted()
	agent.Reply("Ready. <!-- attn:state=idle -->")
	idle := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	w.readTranscripts()

	setSetting(t, app, "crew.heartbeat_lead_seconds", "3600")
	w.advance(2 * time.Minute)
	if got := agent.Prompted(); !strings.Contains(got, crewHeartbeat) || !agent.ThroughMod {
		t.Fatalf("the day took %q through the mod=%t, want the heartbeat through the mod", got, agent.ThroughMod)
	}
	working := testworld.AwaitStateAfter(app, idle, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	runEnds(w, app, agent, working, "Still here. <!-- attn:state=idle -->")

	runs := runRecords(t, w, app, session)
	if len(runs) == 0 || runs[len(runs)-1].StartedBy != "heartbeat" {
		t.Fatalf("run records %v, want the last with started_by=heartbeat", runs)
	}
}
