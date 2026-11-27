package daemon_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const supervisorQuestion = "Decide whether this agent's turn should open to the user now."

func supervisorWorld(t *testing.T, answer func(*fakeagent.HeadlessTask)) (*world, *testworld.Peer, chan string) {
	t.Helper()
	judged := make(chan string, 8)
	w := newTitlingWorld(t, fakeagent.Claude)
	w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
		switch {
		case strings.Contains(task.Prompt, supervisorQuestion):
			judged <- task.Prompt
			answer(task)
		case strings.Contains(task.Prompt, "Classify how this assistant message ends"):
			task.Answer(`{"verdict":"DONE"}`)
		default:
			task.Answer("Refund flow")
		}
	})
	app := w.App()
	setSetting(t, app, "queue_mode_enabled", "true")
	setAnalyticsEnabled(app, "true")
	registerSessions(t, w, w.Client(), "reviewer")
	return w, app, judged
}

type attentionVerdictRecord struct {
	Checkpoint string `json:"checkpoint"`
	Verdict    string `json:"verdict"`
	Decider    string `json:"decider"`
	Label      string `json:"label"`
	Failure    string `json:"failure"`
	FollowUp   string `json:"follow_up"`
}

func (r attentionVerdictRecord) String() string {
	return r.Checkpoint + " " + r.Verdict + " (" + r.Label + ") by " + r.Decider + " failure=" + r.Failure + " follow_up=" + r.FollowUp
}

func runStreamOnceTheDaemonHasStopped(t *testing.T, w *world, session string) (verdicts []string, quietStops int) {
	t.Helper()
	w.restart()
	secret, err := hex.DecodeString(strings.TrimSpace(readSecret(t, filepath.Join(w.Dir, "analytics-secret"))))
	if err != nil {
		t.Fatal(err)
	}
	subject := analytics.HashSubject(secret, session)
	for _, record := range streamOutbox(t, w.App(), analytics.StreamRun) {
		if record.Subject != subject {
			continue
		}
		switch record.Name {
		case "quiet_stop":
			quietStops++
		case "attention_verdict":
			var verdict attentionVerdictRecord
			if err := json.Unmarshal(record.Props, &verdict); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(record.Props), "reason") {
				t.Errorf("an attention record holds the verdict's reason: %s", record.Props)
			}
			verdicts = append(verdicts, verdict.String())
		}
	}
	return verdicts, quietStops
}

func decisionLog(t *testing.T, w *world, session string) []attention.LogEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(w.Dir, attention.DecisionLogFile))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var entries []attention.LogEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry attention.LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decision log line %q: %v", line, err)
		}
		if string(entry.Session) == session && entry.Kind == attention.LogVerdict {
			entries = append(entries, entry)
		}
	}
	return entries
}

func settledAgent(t *testing.T, w *world, app *testworld.Peer) (string, *fakeagent.Run, protocol.Session) {
	t.Helper()
	session, agent, working := mailAgentAtWork(w, app, "shop", "plan the refund flow")
	return session, agent, settleFromApp(app, runEnds(w, app, agent, working, "Planned the refund flow."))
}

func judgedSoFar(judged chan string) []string {
	var prompts []string
	for {
		select {
		case prompt := <-judged:
			prompts = append(prompts, prompt)
		default:
			return prompts
		}
	}
}

func elapsedCheckpoints(t *testing.T, w *world, session string) []attention.LogEntry {
	t.Helper()
	var elapsed []attention.LogEntry
	for _, entry := range decisionLog(t, w, session) {
		if entry.Checkpoint.Elapsed() {
			elapsed = append(elapsed, entry)
		}
	}
	return elapsed
}

func judgedCount(judged chan string) int {
	return len(judgedSoFar(judged))
}

func TestAQuietStopIsJudgedOnceFifteenMinutesLaterAndOpensNothing(t *testing.T) {
	w, app, judged := supervisorWorld(t, func(task *fakeagent.HeadlessTask) {
		task.Answer(`{"verdict":"OPEN","label":"unread report","reason":"the fix landed and only this session reports it"}`)
	})
	session, agent, settled := settledAgent(t, w, app)
	working, _ := notificationRunStarts(t, app, w.Client(), agent, session, settled, "the review is in")
	stopped := runEnds(w, app, agent, working, "The review found one bug, so I fixed it and landed it.")
	if protocol.Deref(stopped.TurnOwed) {
		t.Fatalf("the quiet stop opened a turn: %+v", stopped)
	}

	w.awaitTimer(daemon.AttentionStopTimer)
	w.advance(14 * time.Minute)
	if n := judgedCount(judged); n != 0 {
		t.Fatalf("the supervisor judged the stop %d times before 15 minutes passed", n)
	}
	w.advance(time.Minute)
	prompts := judgedSoFar(judged)
	if len(prompts) != 1 || !strings.Contains(prompts[0], "I fixed it and landed it.") || !strings.Contains(prompts[0], `"checkpoint": "stop"`) {
		t.Fatalf("after 15 minutes the supervisor judged %q, want the stop once with its last message", prompts)
	}
	if after := queriedSession(t, w.Client(), session); protocol.Deref(after.TurnOwed) {
		t.Errorf("an open verdict in shadow opened a turn: %+v", after)
	}

	w.advance(30 * time.Minute)
	verdicts, quietStops := runStreamOnceTheDaemonHasStopped(t, w, session)
	if want := []string{"stop open (unread report) by headless/claude-haiku-fake failure= follow_up=nothing"}; !slices.Equal(verdicts, want) {
		t.Errorf("attention records %q, want %q", verdicts, want)
	}
	if quietStops != 0 {
		t.Errorf("the run stream still holds %d quiet_stop records", quietStops)
	}
	w.restart()
	if n := judgedCount(judged); n != 0 {
		t.Errorf("restarts judged the same facts %d more times", n)
	}
	if logged := decisionLog(t, w, session); len(logged) != 1 || logged[0].Checkpoint != attention.CheckpointStop || logged[0].Verdict != attention.Open {
		t.Errorf("the decision log holds %+v, want the one stop verdict", logged)
	}
	if after := queriedSession(t, w.Client(), session); protocol.Deref(after.TurnOwed) {
		t.Errorf("a turn is open after the restarts: %+v", after)
	}
}

func TestARunInTheQuietStopsWaitCancelsItsCheckpoint(t *testing.T) {
	w, app, judged := supervisorWorld(t, func(task *fakeagent.HeadlessTask) { task.Answer(`{"verdict":"QUIET","reason":"waiting"}`) })
	session, agent, settled := settledAgent(t, w, app)
	working, _ := notificationRunStarts(t, app, w.Client(), agent, session, settled, "the review is in")
	stopped := runEnds(w, app, agent, working, "Noted the review.")
	w.awaitTimer(daemon.AttentionStopTimer)
	w.advance(10 * time.Minute)
	working, _ = notificationRunStarts(t, app, w.Client(), agent, session, stopped, "the deploy is in")
	runEnds(w, app, agent, working, "Noted the deploy.")
	w.awaitTimer(daemon.AttentionStopTimer)
	w.advance(10 * time.Minute)
	if n := judgedCount(judged); n != 0 {
		t.Fatalf("the supervisor judged %d times 10 minutes after the second stop", n)
	}
	w.advance(5 * time.Minute)
	if prompts := judgedSoFar(judged); len(prompts) != 1 || !strings.Contains(prompts[0], "Noted the deploy.") {
		t.Errorf("the supervisor judged %q, want only the second stop", prompts)
	}
}

func TestAFailedOrRefusedModelCallRecordsQuietWithNoDecider(t *testing.T) {
	t.Run("the model call fails", func(t *testing.T) {
		w, app, judged := supervisorWorld(t, func(task *fakeagent.HeadlessTask) { task.Fail("You've hit your weekly limit") })
		session, agent, settled := settledAgent(t, w, app)
		working, _ := notificationRunStarts(t, app, w.Client(), agent, session, settled, "the review is in")
		runEnds(w, app, agent, working, "Fixed and landed it.")
		w.awaitTimer(daemon.AttentionStopTimer)
		w.advance(attentionStopWait)
		w.advance(attentionFollowUpWindow)
		if n := judgedCount(judged); n != 1 {
			t.Errorf("the supervisor asked the model %d times, want 1", n)
		}
		verdicts, _ := runStreamOnceTheDaemonHasStopped(t, w, session)
		if want := []string{"stop quiet () by none failure=harness follow_up=nothing"}; !slices.Equal(verdicts, want) {
			t.Errorf("attention records %q, want %q", verdicts, want)
		}
	})
	t.Run("headless tasks are off", func(t *testing.T) {
		w := newWorld(t, fakeagent.Claude)
		w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
			t.Errorf("a headless task ran with headless tasks off: %q", task.Prompt)
		})
		app := w.App()
		setSetting(t, app, "queue_mode_enabled", "true")
		setAnalyticsEnabled(app, "true")
		registerSessions(t, w, w.Client(), "reviewer")
		session, agent, working := mailAgentAtWork(w, app, "shop", "plan the refund flow")
		settled := settleFromApp(app, runEnds(w, app, agent, working, "Planned the refund flow. <!-- attn:state=idle -->"))
		working, _ = notificationRunStarts(t, app, w.Client(), agent, session, settled, "the review is in")
		runEnds(w, app, agent, working, "Fixed and landed it. <!-- attn:state=idle -->")
		w.awaitTimer(daemon.AttentionStopTimer)
		w.advance(attentionStopWait)
		w.advance(attentionFollowUpWindow)
		verdicts, _ := runStreamOnceTheDaemonHasStopped(t, w, session)
		if want := []string{"stop quiet () by none failure=headless_tasks_off follow_up=nothing"}; !slices.Equal(verdicts, want) {
			t.Errorf("attention records %q, want %q", verdicts, want)
		}
	})
}

const (
	attentionStopWait       = 15 * time.Minute
	attentionFollowUpWindow = 30 * time.Minute
)

func TestAVerdictsFollowUpIsTheUsersNextRun(t *testing.T) {
	w, app, _ := supervisorWorld(t, func(task *fakeagent.HeadlessTask) { task.Answer(`{"verdict":"OPEN","reason":"a result to read"}`) })
	session, agent, settled := settledAgent(t, w, app)
	working, _ := notificationRunStarts(t, app, w.Client(), agent, session, settled, "the review is in")
	stopped := runEnds(w, app, agent, working, "Fixed and landed it.")
	w.awaitTimer(daemon.AttentionStopTimer)
	w.advance(20 * time.Minute)
	working = userRunStarts(app, agent, session, stopped, "thanks, what's next?")
	runEnds(w, app, agent, working, "The tax rounding is next.")
	verdicts, _ := runStreamOnceTheDaemonHasStopped(t, w, session)
	if want := []string{"stop open () by headless/claude-haiku-fake failure= follow_up=typed"}; !slices.Equal(verdicts, want) {
		t.Errorf("attention records %q, want %q", verdicts, want)
	}
}

func TestABackgroundWaitIsJudgedTwentyMinutesAfterTheLastModelRequest(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "s1")
		terminal := protocol.TerminalID(w.Terminal("s1"))
		take := func(prompt string) {
			if err := cli.UpdateStateFromHookEvidence(terminal, protocol.StateWorking, "", "user_prompt_submit", prompt); err != nil {
				t.Fatal(err)
			}
			w.advance(0)
		}
		take("deploy staging")
		deploy := protocol.BackgroundTask{ID: protocol.Ptr("bdeploy"), Kind: protocol.Ptr("shell"), Description: protocol.Ptr("Deploy staging"), Command: protocol.Ptr("./deploy.sh staging"), Wakes: protocol.Ptr(protocol.BackgroundTaskWakeExit)}
		if err := cli.SendStop(terminal, "", client.StopFacts{BackgroundTasks: []protocol.BackgroundTask{deploy}}); err != nil {
			t.Fatal(err)
		}
		w.advance(19 * time.Minute)
		if logged := elapsedCheckpoints(t, w, "s1"); len(logged) != 0 {
			t.Fatalf("19 minutes into the wait the decision log holds %+v", logged)
		}
		w.advance(time.Minute)
		if logged := elapsedCheckpoints(t, w, "s1"); len(logged) != 1 || !strings.Contains(string(logged[0].Facts), "./deploy.sh staging") {
			t.Fatalf("20 minutes into the wait the decision log holds %+v, want one elapsed_20m checkpoint with the deploy", logged)
		}

		w.advance(30 * time.Minute)
		take("the deploy needs a retry")
		if err := cli.SendStop(terminal, "", client.StopFacts{BackgroundTasks: []protocol.BackgroundTask{deploy}}); err != nil {
			t.Fatal(err)
		}
		w.advance(15 * time.Minute)
		if logged := elapsedCheckpoints(t, w, "s1"); len(logged) != 1 {
			t.Fatalf("before 20 minutes passed since the new request the decision log holds %d elapsed checkpoints, want the first alone", len(logged))
		}
		w.advance(5 * time.Minute)
		if logged := elapsedCheckpoints(t, w, "s1"); len(logged) != 2 || logged[1].Checkpoint != attention.CheckpointElapsed20m {
			t.Errorf("20 minutes after the new request the decision log holds %+v, want a second elapsed_20m checkpoint", logged)
		}
	})
}

func TestBackgroundFactsAreUnknownWhenTheHarnessDoesNotSay(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	cwd := registerDelegationCaller(t, w, cli, "caller")
	delegated, err := cli.Delegate(delegateFrom("caller", cwd, "Migrate the first table", fakeagent.Codex))
	if err != nil {
		t.Fatal(err)
	}
	delegate := string(delegated.SessionID)
	run := w.Launched(delegate)
	run.Prompted()
	run.Reply("Migrated it. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, delegate, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if _, raw := attentionFactsOf(t, cli, delegate); !strings.Contains(raw, `"background":"unknown"`) {
		t.Errorf("a Codex agent's facts are %s, want its background unknown", raw)
	}
}
