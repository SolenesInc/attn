package daemon_test

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func newDictationWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	t.Setenv("ATTN_HEADLESS_TASKS", "on")
	return w
}

func dictationRefine(app *testworld.Peer, text string, recording time.Duration) protocol.DictationRefineMessage {
	msg := protocol.DictationRefineMessage{
		Cmd: protocol.CmdDictationRefine, RequestID: uuid.NewString(), ProfileID: app.SelectedProfile(),
		RawText: text, RecordingDurationMs: int(recording.Milliseconds()), Format: protocol.DictationTextFormatPlain,
	}
	app.Send(msg)
	return msg
}

func dictationResult(app *testworld.Peer, requestID string) protocol.DictationRefineResultMessage {
	app.T.Helper()
	return testworld.Await(app, protocol.EventDictationRefineResult, func(m protocol.DictationRefineResultMessage) bool { return m.RequestID == requestID })
}

func dictationResultsFor(app *testworld.Peer, requestID string) int {
	count := 0
	for _, raw := range app.Log() {
		var m protocol.DictationRefineResultMessage
		if json.Unmarshal(raw, &m) == nil && m.Event == protocol.EventDictationRefineResult && m.RequestID == requestID {
			count++
		}
	}
	return count
}

func lightModel(t *testing.T, app *testworld.Peer, harness string) string {
	t.Helper()
	found := testworld.Request(app, protocol.HarnessModelsMessage{Cmd: protocol.CmdHarnessModels, Harness: harness, RequestID: "light-" + harness},
		protocol.EventHarnessModelsResult, func(m protocol.HarnessModelsResultMessage) bool { return m.RequestID == "light-"+harness })
	return protocol.Deref(found.TierDefaults.Light)
}

func pendingTimers(w *world) []string {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	seen, _ := w.clock.AwaitTimer(ctx, "no-such-timer")
	return seen
}

func TestDictationRefinementRunsToolFreeOnTheChosenHarnessLightModel(t *testing.T) {
	w := newDictationWorld(t)
	app := w.TrustedApp()
	for _, tc := range []struct {
		setting  string
		harness  fakeagent.Harness
		toolFree []string
	}{
		{setting: "", harness: fakeagent.Claude, toolFree: []string{"--disallowedTools", "*"}},
		{setting: "codex", harness: fakeagent.Codex, toolFree: []string{"--sandbox", "read-only"}},
		{setting: "claude", harness: fakeagent.Claude, toolFree: []string{"--disallowedTools", "*"}},
	} {
		setSetting(t, app, "dictation.refinement_agent", tc.setting)
		spoken := "um so i think we should keep the raw text around"
		request := dictationRefine(app, spoken, 4*time.Second)
		task := w.HeadlessTask()
		if task.Harness != tc.harness {
			t.Fatalf("with setting %q the refinement ran on %s, want %s", tc.setting, task.Harness, tc.harness)
		}
		if want := lightModel(t, app, string(tc.harness)); task.Model != want {
			t.Errorf("%s refined with model %q, want its light model %q", tc.harness, task.Model, want)
		}
		if !hasArgs(task.Argv, tc.toolFree...) {
			t.Errorf("%s refined without %q: %q", tc.harness, tc.toolFree, task.Argv)
		}
		if !strings.Contains(task.Prompt, `{"transcript":"`+spoken+`"}`) {
			t.Errorf("%s got prompt %q, want the transcript as JSON data", tc.harness, task.Prompt)
		}
		task.Answer("  I think we should keep the raw text around.\n")
		got := dictationResult(app, request.RequestID)
		if !got.Success || protocol.Deref(got.RefinedText) != "I think we should keep the raw text around." || got.ProfileID != request.ProfileID || got.LimitMs != nil {
			t.Fatalf("%s result = %+v, want the trimmed refinement for the requesting profile", tc.harness, got)
		}
		if _, err := os.Stat(task.Dir); !os.IsNotExist(err) {
			t.Errorf("%s left its scratch directory %s behind: %v", tc.harness, task.Dir, err)
		}
	}
}

func TestDictationRefinementExpiresAtFifteenSecondsPlusAQuarterOfTheRecording(t *testing.T) {
	w := newDictationWorld(t)
	app := w.TrustedApp()
	for _, tc := range []struct {
		recording time.Duration
		limit     time.Duration
		message   string
	}{
		{recording: 0, limit: 15 * time.Second, message: "within 15 s for this 0 s recording"},
		{recording: 40 * time.Second, limit: 25 * time.Second, message: "within 25 s for this 40 s recording"},
		{recording: 143144 * time.Millisecond, limit: 50786 * time.Millisecond, message: "within 50.8 s for this 143.1 s recording"},
	} {
		request := dictationRefine(app, "deploy to staging", tc.recording)
		w.awaitTimer(daemon.DictationRefineDeadlineTimer)
		task := w.HeadlessTask()
		armed := "dictation-refine-deadline " + tc.limit.String()
		if pending := pendingTimers(w); !slices.Contains(pending, armed) {
			t.Fatalf("pending timers %q, want %q for a %s recording", pending, armed, tc.recording)
		}
		w.advance(tc.limit - time.Millisecond)
		if pending := pendingTimers(w); !slices.Contains(pending, armed) {
			t.Fatalf("the %s limit fired early; pending timers %q", tc.limit, pending)
		}
		w.advance(time.Millisecond)
		got := dictationResult(app, request.RequestID)
		if got.Success || protocol.Deref(got.LimitMs) != int(tc.limit.Milliseconds()) ||
			!strings.Contains(protocol.Deref(got.Error), tc.message) || !strings.Contains(protocol.Deref(got.Error), "Original kept") {
			t.Fatalf("expired result = %+v (error %q), want limit %s named as %q", got, protocol.Deref(got.Error), tc.limit, tc.message)
		}
		task.AwaitExited()
		task.Answer("Deploy to staging.")
	}

	request := dictationRefine(app, "and then roll back", time.Second)
	w.HeadlessTask().Answer("And then roll back.")
	if got := dictationResult(app, request.RequestID); !got.Success {
		t.Fatalf("a fresh request after the expiries failed: %s", protocol.Deref(got.Error))
	}
	for _, raw := range app.Log() {
		var m protocol.DictationRefineResultMessage
		if json.Unmarshal(raw, &m) == nil && m.Event == protocol.EventDictationRefineResult && m.Success && m.RequestID != request.RequestID {
			t.Errorf("an expired request later succeeded: %+v", m)
		}
	}
}

func TestCancellingOrDisconnectingStopsTheRefinementWithoutAnAnswer(t *testing.T) {
	w := newDictationWorld(t)
	app := w.TrustedApp()

	cancelled := dictationRefine(app, "first thought", 2*time.Second)
	held := w.HeadlessTask()
	app.Send(protocol.DictationRefineCancelMessage{Cmd: protocol.CmdDictationRefineCancel, RequestID: cancelled.RequestID})
	held.AwaitExited()
	held.Answer("First thought.")

	other := w.TrustedApp()
	shared := dictationRefine(other, "second thought", 2*time.Second)
	sharedTask := w.HeadlessTask()
	app.Send(protocol.DictationRefineCancelMessage{Cmd: protocol.CmdDictationRefineCancel, RequestID: shared.RequestID})
	next := dictationRefine(app, "third thought", 2*time.Second)
	nextTask := w.HeadlessTask()
	nextTask.Answer("Third thought.")
	if got := dictationResult(app, next.RequestID); protocol.Deref(got.RefinedText) != "Third thought." {
		t.Fatalf("the next refinement answered %+v", got)
	}
	sharedTask.Answer("Second thought.")
	if got := dictationResult(other, shared.RequestID); protocol.Deref(got.RefinedText) != "Second thought." {
		t.Fatalf("another app's cancel reached this app's refinement: %+v", got)
	}
	if n := dictationResultsFor(app, cancelled.RequestID); n != 0 {
		t.Errorf("the cancelled refinement answered %d times", n)
	}
	if n := dictationResultsFor(app, shared.RequestID); n != 0 {
		t.Errorf("the app received %d results for another app's refinement", n)
	}

	leaving := w.TrustedApp()
	dictationRefine(leaving, "last thought", 2*time.Second)
	abandoned := w.HeadlessTask()
	leaving.Close()
	abandoned.AwaitExited()
	abandoned.Answer("Last thought.")
}

func TestDictationRefinementRefusesWhatItCannotRun(t *testing.T) {
	w := newDictationWorld(t)
	app := w.TrustedApp()

	untrusted := w.App()
	refused := dictationRefine(untrusted, "hello", time.Second)
	if got := dictationResult(untrusted, refused.RequestID); got.Success || !strings.Contains(protocol.Deref(got.Error), "authenticated attn app") {
		t.Errorf("an unauthenticated client got %+v", got)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*protocol.DictationRefineMessage)
		want   string
	}{
		{"other profile", func(m *protocol.DictationRefineMessage) { m.ProfileID = "someone-else" }, "came from an app showing profile"},
		{"negative duration", func(m *protocol.DictationRefineMessage) { m.RecordingDurationMs = -1 }, "must not be negative, got -1"},
		{"no text", func(m *protocol.DictationRefineMessage) { m.RawText = "  " }, "needs raw_text"},
		{"unknown format", func(m *protocol.DictationRefineMessage) { m.Format = "html" }, `plain or markdown, not "html"`},
	} {
		msg := protocol.DictationRefineMessage{Cmd: protocol.CmdDictationRefine, RequestID: uuid.NewString(), ProfileID: app.SelectedProfile(),
			RawText: "hello", RecordingDurationMs: 1000, Format: protocol.DictationTextFormatMarkdown}
		tc.mutate(&msg)
		app.Send(msg)
		if got := dictationResult(app, msg.RequestID); got.Success || !strings.Contains(protocol.Deref(got.Error), tc.want) {
			t.Errorf("%s: result %+v, want an error naming %q", tc.name, got, tc.want)
		}
	}

	setSetting(t, app, "headless_tasks.enabled", "false")
	t.Setenv("ATTN_HEADLESS_TASKS", "")
	off := dictationRefine(app, "hello", time.Second)
	if got := dictationResult(app, off.RequestID); got.Success || !strings.Contains(protocol.Deref(got.Error), "headless tasks are off") {
		t.Errorf("with headless tasks off the refinement answered %+v", got)
	}
}

func TestRefinementFailuresNameTheirCause(t *testing.T) {
	w := newDictationWorld(t)
	app := w.TrustedApp()
	for _, tc := range []struct {
		harness string
		failure string
		want    string
	}{
		{"claude", "API Error: Connection refused — a firewall or proxy may be blocking it (ECONNREFUSED)", "claude: headless agent could not reach its model provider"},
		{"claude", "Not logged in · Please run /login", "claude: headless agent authentication failed"},
		{"claude", "There's an issue with the selected model (claude-haiku-fake). It may not exist or you may not have access to it.", "claude: headless agent model is invalid or unavailable"},
		{"claude", "stream ended unexpectedly", "claude: headless agent process failed"},
		{"codex", "The 'gpt-6-luna' model is not supported when using Codex with a ChatGPT account.", "codex: headless agent model is invalid or unavailable"},
		{"codex", "stream ended unexpectedly", "codex: headless agent process failed"},
	} {
		setSetting(t, app, "dictation.refinement_agent", tc.harness)
		request := dictationRefine(app, "hello", time.Second)
		w.HeadlessTask().Fail(tc.failure)
		got := dictationResult(app, request.RequestID)
		if got.Success || !strings.Contains(protocol.Deref(got.Error), tc.want) || got.LimitMs != nil {
			t.Errorf("%s failing with %q answered %q, want %q", tc.harness, tc.failure, protocol.Deref(got.Error), tc.want)
		}
	}

	setSetting(t, app, "dictation.refinement_agent", "claude")
	empty := dictationRefine(app, "hello", time.Second)
	w.HeadlessTask().Answer("   ")
	if got := dictationResult(app, empty.RequestID); got.Success || !strings.Contains(protocol.Deref(got.Error), "claude returned no text") {
		t.Errorf("an empty answer gave %+v", got)
	}
}
