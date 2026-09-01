package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessionstate"
)

func TestStopIsNonTerminal(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		crons    int
		relax    bool
		want     bool
	}{
		{
			name:     "background work still running",
			statuses: []string{"running"},
			want:     true,
		},
		{
			name:  "parked on a scheduled wakeup: the turn still ended",
			crons: 1,
			want:  false,
		},
		{
			name:     "both outstanding",
			statuses: []string{"running"},
			crons:    1,
			want:     true,
		},
		{
			name:     "a finished background task is not outstanding work",
			statuses: []string{"completed"},
			want:     false,
		},
		{
			name:     "mixed statuses count as running if any is",
			statuses: []string{"completed", "running"},
			want:     true,
		},
		{
			name:     "status casing is the harness's, not ours",
			statuses: []string{"Running"},
			want:     true,
		},
		{
			name: "nothing outstanding: the turn ended",
			want: false,
		},
		{
			name:     "chief relax: its background work does not defer the end of the turn",
			statuses: []string{"running"},
			relax:    true,
			want:     false,
		},
		{
			name:     "chief relax: a parked schedule does not defer it either",
			statuses: []string{"running"},
			crons:    1,
			relax:    true,
			want:     false,
		},
		{
			name:  "chief relax: cron only, still a real end of turn",
			crons: 1,
			relax: true,
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &protocol.StopMessage{
				Cmd:             protocol.CmdStop,
				ID:              "sess",
				BackgroundTasks: tasksWithStatuses(tc.statuses...),
			}
			if tc.crons > 0 {
				msg.PendingSessionCrons = protocol.Ptr(tc.crons)
			}
			if got := stopIsNonTerminal(msg, tc.relax); got != tc.want {
				t.Fatalf("stopIsNonTerminal() = %v, want %v", got, tc.want)
			}
		})
	}
}

func tasksWithStatuses(statuses ...string) []protocol.StopBackgroundTask {
	tasks := make([]protocol.StopBackgroundTask, 0, len(statuses))
	for _, status := range statuses {
		tasks = append(tasks, protocol.StopBackgroundTask{Type: "background_session", Status: status})
	}
	return tasks
}

func TestDescribeBackgroundTasks(t *testing.T) {
	msg := &protocol.StopMessage{BackgroundTasks: []protocol.StopBackgroundTask{
		{Type: "background_session", Status: "running", Name: protocol.Ptr("gh run watch 1234")},
		{Type: "cron", Status: "pending"},
	}}
	want := `background_session running "gh run watch 1234", cron pending`
	if got := describeBackgroundTasks(msg); got != want {
		t.Fatalf("describeBackgroundTasks() = %q, want %q", got, want)
	}
	if got := describeBackgroundTasks(&protocol.StopMessage{}); got != "" {
		t.Fatalf("describeBackgroundTasks(empty) = %q, want empty", got)
	}
}

func TestStopIsNonTerminal_LegacyHookClassifies(t *testing.T) {
	msg := &protocol.StopMessage{Cmd: protocol.CmdStop, ID: "sess", TranscriptPath: "/tmp/t.jsonl"}
	if stopIsNonTerminal(msg, false) {
		t.Fatal("a legacy hook reports neither fact; the stop must read as terminal")
	}
}

// Boundary-bound: runs a started daemon, a real unix listener and a real client;
// the yielded-stop siblings bubble because they drive handleStop directly.
func TestDaemon_StopCommand_BackgroundWork_StaysWorking(t *testing.T) {
	useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "attn.sock")
	os.Remove(sockPath)

	d := NewForTesting(sockPath)
	go d.Start()
	defer func() {
		d.Stop()
		os.Remove(sockPath)
	}()
	waitForSocket(t, sockPath, 5*time.Second)

	c := client.New(sockPath)
	if err := c.Register("bg-session", "Test", "/tmp/test"); err != nil {
		t.Fatalf("Register error: %v", err)
	}

	if err := c.SendStop("bg-session", "/nonexistent/transcript.jsonl", client.StopFacts{
		BackgroundTasks: tasksWithStatuses("running"),
	}); err != nil {
		t.Fatalf("SendStop error: %v", err)
	}

	waitForResolvedState(t, d, "bg-session", protocol.SessionStateWorking)
}

// Boundary-bound: started daemon and a real socket, as above.
func TestDaemon_StopCommand_PendingCron_Settles(t *testing.T) {
	useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "attn.sock")
	os.Remove(sockPath)

	d := NewForTesting(sockPath)
	go d.Start()
	defer func() {
		d.Stop()
		os.Remove(sockPath)
	}()
	waitForSocket(t, sockPath, 5*time.Second)

	c := client.New(sockPath)
	if err := c.Register("cron-session", "Test", "/tmp/test"); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if err := c.SendStop("cron-session", "/nonexistent/transcript.jsonl", client.StopFacts{
		PendingSessionCrons: 1,
	}); err != nil {
		t.Fatalf("SendStop error: %v", err)
	}

	waitForResolvedState(t, d, "cron-session", protocol.SessionStateIdle)
}

type recordingClassifier struct {
	state string
	mu    sync.Mutex
	texts []string
}

func (c *recordingClassifier) Classify(text string, timeout time.Duration) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.texts = append(c.texts, text)
	return c.state, nil
}

func (c *recordingClassifier) Texts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.texts...)
}

func yieldedStopDaemon(t *testing.T, d *Daemon, verdict string) (*Daemon, *recordingClassifier) {
	t.Helper()
	judge := &recordingClassifier{state: verdict}
	d.classifier = judge
	d.classificationTranscriptExtractor = func(*protocol.Session, string, int, time.Time) (string, string, error) {
		return "The profile build is still running in the background; I'll continue when it completes.", "turn-1", nil
	}

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "yielded",
		Agent:          protocol.SessionAgentClaude,
		Label:          "test",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
		Todos:          []string{"[→] wait for the build", "[ ] verify live"},
	})
	d.recordBracketEvidence("yielded", protocol.StateWorking)
	d.recordPTYEvidence("yielded", pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: now.Add(-time.Second)})
	d.recordPTYEvidence("yielded", pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: now})

	d.handleStop(drainingConn(t), &protocol.StopMessage{
		Cmd:             protocol.CmdStop,
		ID:              "yielded",
		TranscriptPath:  "/tmp/transcript.jsonl",
		BackgroundTasks: tasksWithStatuses("running"),
	})

	// The judgment is dispatched async on the retry loop handleStop owns; run it out
	// or the verdict is never coming.
	settleStopClassification(t)
	if e, ok := d.evidenceTable().snapshot("yielded"); !ok || e.LastClassifier == nil {
		t.Fatalf("yield verdict never landed as evidence (classifier calls: %d)", len(judge.Texts()))
	}
	return d, judge
}

func TestDaemon_YieldedStop_ParkedVerdictHoldsWorkingPastPromptIdle(t *testing.T) {
	base := NewForTesting(filepath.Join(shortTempDir(t), "test.sock"))
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, base)
		d, judge := yieldedStopDaemon(t, base, classifier.VerdictParked)

		texts := judge.Texts()
		if len(texts) != 1 || !strings.Contains(texts[0], "[harness facts]") {
			t.Fatalf("judge input missing the harness-facts line: %q", texts)
		}

		d.recordNotificationEvidence("yielded", notifyIdlePrompt, "Claude is waiting for your input")
		d.resolveAllSessions(time.Now())

		sess := d.store.Get("yielded")
		if sess == nil {
			t.Fatal("session missing after resolve")
		}
		if sess.State != protocol.StateWorking {
			t.Fatalf("state = %s, want %s: a parked verdict outranks the prompt-idle confirmation", sess.State, protocol.StateWorking)
		}
	})
}

func TestDaemon_YieldedStop_ParkedVerdictExpiresIntoPromptIdle(t *testing.T) {
	base := NewForTesting(filepath.Join(shortTempDir(t), "test.sock"))
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, base)
		d, _ := yieldedStopDaemon(t, base, classifier.VerdictParked)
		d.recordNotificationEvidence("yielded", notifyIdlePrompt, "Claude is waiting for your input")

		policy := sessionstate.PolicyFor(string(protocol.SessionAgentClaude))
		time.Sleep(policy.ParkedAfter - time.Minute)
		d.resolveAllSessions(time.Now())
		if got := d.store.Get("yielded").State; got != protocol.StateWorking {
			t.Fatalf("state before the tripwire = %s, want %s", got, protocol.StateWorking)
		}

		time.Sleep(2 * time.Minute)
		d.resolveAllSessions(time.Now())
		sess := d.store.Get("yielded")
		if sess.State != protocol.StateIdle {
			t.Fatalf("state past the tripwire = %s, want %s", sess.State, protocol.StateIdle)
		}
		if got := sessionstate.Reason(protocol.Deref(d.sessionForBroadcast(sess).StateReason)); got != sessionstate.ReasonParkedExpired {
			t.Fatalf("state reason = %q, want %q", got, sessionstate.ReasonParkedExpired)
		}
	})
}

func TestDaemon_YieldedStop_DoneVerdictSettles(t *testing.T) {
	base := NewForTesting(filepath.Join(shortTempDir(t), "test.sock"))
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, base)
		d, _ := yieldedStopDaemon(t, base, protocol.StateIdle)

		d.resolveAllSessions(time.Now())

		sess := d.store.Get("yielded")
		if sess == nil {
			t.Fatal("session missing after resolve")
		}
		if sess.State != protocol.StateIdle {
			t.Fatalf("state = %s, want %s: an idle verdict on a yield means the running process is a leftover", sess.State, protocol.StateIdle)
		}
	})
}

// Nothing applies a state at the moment a source speaks, so reading the store
// straight after the socket call asserts on the resolve tick timing.
func waitForResolvedState(t *testing.T, d *Daemon, sessionID string, want protocol.SessionState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last protocol.SessionState
	for time.Now().Before(deadline) {
		if session := d.store.Get(sessionID); session != nil {
			last = session.State
			if last == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("session %s state = %s, want %s", sessionID, last, want)
}
