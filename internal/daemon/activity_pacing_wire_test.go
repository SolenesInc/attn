package daemon_test

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type scriptedActivityRuns struct {
	mu       sync.Mutex
	prompts  []string
	failNext bool
}

func (l *scriptedActivityRuns) answer(task *fakeagent.HeadlessTask) {
	if strings.HasPrefix(task.Prompt, "Classify how this assistant message ends") {
		task.Answer(`{"verdict":"DONE"}`)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prompts = append(l.prompts, task.Prompt)
	if l.failNext {
		l.failNext = false
		task.Fail("the service is overloaded")
		return
	}
	task.Answer("Working on checkout")
}

func (l *scriptedActivityRuns) asked() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.prompts...)
}

func (l *scriptedActivityRuns) failTheNext() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failNext = true
}

func TestAWorkingSessionsActivityLineIsWrittenOnlyForNewOutputAndAtMostOncePerInterval(t *testing.T) {
	lines := &scriptedActivityRuns{}
	inBubbleAnsweringHeadlessTasks(t, []fakeagent.Harness{fakeagent.Claude}, func(t *testing.T, w *world) {
		w.AnswerHeadlessTasks(lines.answer)
		app, cli := w.App(), w.Client()
		setSetting(t, app, "activity.enabled", "true")
		setSetting(t, app, "activity.config", `{"agent":"claude"}`)
		setSetting(t, app, "activity.intervals", `{"watching":120,"present":300}`)
		cwd := w.Path("s1")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		transcript := fakeagent.WriteClaudeTranscript(t, cwd)
		transcript.Answer("Reading the checkout plan.")
		if err := cli.RegisterWithAgent("s1", "checkout work", cwd, string(protocol.SessionAgentClaude)); err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := cli.ObserveAgentConversation("s1", transcript.ConversationID, transcript.Path); err != nil {
			t.Fatalf("bind the conversation: %v", err)
		}
		expect := func(when string, want int) []string {
			t.Helper()
			asked := lines.asked()
			if len(asked) != want {
				t.Fatalf("%s the activity line was generated %d times, want %d", when, len(asked), want)
			}
			return asked
		}

		watchActivity(w, app, true, time.Minute)
		expect("for the output written before attn first looked", 0)
		write := func(text string) {
			w.advance(time.Second)
			transcript.Answer(text)
		}

		write("Running the checkout tests.")
		watchActivity(w, app, true, 30*time.Second)
		asked := expect("for the first output after attn first looked", 1)
		if !strings.Contains(asked[0], "Running the checkout tests.") || strings.Contains(asked[0], "Reading the checkout plan.") {
			t.Errorf("the first activity prompt = %q, want only the output since attn first looked", asked[0])
		}

		write("Fixing the cart test.")
		watchActivity(w, app, true, 90*time.Second)
		expect("within the watching interval", 1)
		watchActivity(w, app, true, time.Minute)
		expect("once the watching interval passed", 2)

		looked, _ := activityTask(app, "s1")
		watchActivity(w, app, true, 5*time.Minute)
		expect("while the session wrote nothing", 2)
		if again, _ := activityTask(app, "s1"); again.UpdatedAt != looked.UpdatedAt {
			t.Errorf("while the session wrote nothing attn looked again at %s", again.UpdatedAt)
		}

		lines.failTheNext()
		write("Pushing the branch.")
		watchActivity(w, app, true, 30*time.Second)
		expect("for new output", 3)
		write("Opening the pull request.")
		watchActivity(w, app, true, 30*time.Second)
		expect("right after a failed run", 3)
		watchActivity(w, app, true, 2*time.Minute)
		expect("once the interval after a failed run passed", 4)

		write("Waiting on review.")
		watchActivity(w, app, false, 10*time.Minute)
		expect("while the user was away", 4)
	})
}

func watchActivity(w *world, app *testworld.Peer, visible bool, span time.Duration) {
	w.T.Helper()
	for step := 30 * time.Second; span > 0; span -= step {
		app.Send(protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: visible, DashboardVisible: visible, IdleSeconds: protocol.Ptr(0.0)})
		w.advance(min(step, span))
	}
}
