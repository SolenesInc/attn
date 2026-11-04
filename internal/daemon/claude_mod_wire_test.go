package daemon_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	claudeModSetting     = "claude_mod_enabled"
	claudeModPromptLine  = "› Prompt from the attn plugin"
	claudeModReachWindow = 5 * time.Second
)

func moddedClaude(t *testing.T, w *world, app *testworld.Peer) (string, *fakeagent.Run) {
	t.Helper()
	setSetting(t, app, claudeModSetting, "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	run := w.Launched(session)
	w.awaitTimer(daemon.ClaudeModPollHoldTimer)
	return session, run
}

func workingOn(t *testing.T, app *testworld.Peer, run *fakeagent.Run, session, prompt string) {
	t.Helper()
	run.Input(prompt)
	if got := run.Prompted(); got != prompt {
		t.Fatalf("the agent took %q, want %q", got, prompt)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
}

func stoppedByEsc(t *testing.T, app *testworld.Peer, run *fakeagent.Run, session string) []string {
	t.Helper()
	returned := run.ModEsc()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateIdle && protocol.Deref(s.StateReason) == "turn_aborted"
	})
	return returned
}

func delivered(t *testing.T, app *testworld.Peer, session, text string) {
	t.Helper()
	if result := feedbackResult(app, sendFeedback(app, session, text)); !result.Success || result.Status != "delivered" {
		t.Fatalf("the feedback %q was answered %+v, want it delivered", text, result)
	}
}

func modEvent(t *testing.T, run *fakeagent.Run, kind, text string) {
	t.Helper()
	for {
		event := run.ModNext()
		if event.Kind == kind && strings.Contains(event.Text, text) {
			return
		}
		if event.Kind != fakeagent.ModEventRefusedStop {
			t.Fatalf("the mod reported %+v, want %s of %q", event, kind, text)
		}
	}
}

func typedOnce(t *testing.T, app *testworld.Peer, run *fakeagent.Run, session, text string) {
	t.Helper()
	if got := run.Prompted(); got != text || run.ThroughMod {
		t.Fatalf("the agent took %q (through the mod: %t), want %q typed", got, run.ThroughMod, text)
	}
	nothingElsePrompted(t, app, run, session)
}

func nothingElsePrompted(t *testing.T, app *testworld.Peer, run *fakeagent.Run, session string) {
	t.Helper()
	probe := "probe-" + uuid.NewString()[:8]
	app.TypeLine(session, probe)
	if got := run.Prompted(); got != probe {
		t.Fatalf("the agent took %q before %q, want nothing in between", got, probe)
	}
}

func cacheAskAnswered(t *testing.T, w *world, app *testworld.Peer, session string) {
	t.Helper()
	asked, err := w.Client().AskOpen(protocol.SessionID(session), "", cacheAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 1 })
	answerCacheAsk(t, app, asked.ID)
}

const cacheAnswerText = "Answer: Postgres"

func TestAnIdleAnnotationReachesClaudeThroughTheModWithNoKeystroke(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session, run := moddedClaude(t, w, app)

	delivered(t, app, session, "check the rounding")
	if got := run.Prompted(); got != "check the rounding" || !run.ThroughMod {
		t.Fatalf("the agent took %q (through the mod: %t), want the annotation through the mod", got, run.ThroughMod)
	}
	screen := settledScreen(t, app, session)
	if !strings.Contains(screen, claudeModPromptLine) {
		t.Errorf("the terminal lacks %q:\n%s", claudeModPromptLine, screen)
	}
	if strings.Contains(screen, "check the rounding") {
		t.Errorf("the annotation was typed into the terminal:\n%s", screen)
	}
}

func TestAMessageToAWorkingClaudeIsHeldThenAppendedAtTheToolBoundary(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session, run := moddedClaude(t, w, app)
	workingOn(t, app, run, session, "write the essay")

	delivered(t, app, session, "shorter please")
	modEvent(t, run, fakeagent.ModEventHeld, "shorter please")
	if appended := run.ModToolBoundary(); !slices.Equal(appended, []string{"shorter please"}) {
		t.Fatalf("the tool boundary appended %v, want the held message", appended)
	}

	run.ModReload()
	w.awaitTimer(daemon.ClaudeModPollHoldTimer)
	nothingElsePrompted(t, app, run, session)
}

func TestAfterEscAnInboxRingWaitsUnreadUntilTheUsersNextPrompt(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session, run := moddedClaude(t, w, app)
	registerSessions(t, w, cli, "peer")
	workingOn(t, app, run, session, "write the essay")
	stoppedByEsc(t, app, run, session)

	sent := sendAgentMessage(t, cli, "peer", session, "the schema changed")
	if !strings.Contains(sent.Detail, "next prompt") {
		t.Errorf("the sender was told %q, want it to say the message waits for the user's next prompt", sent.Detail)
	}
	modEvent(t, run, fakeagent.ModEventRefusedStop, inboxDoorbell)
	status, err := cli.AgentMsgStatus(sent.MessageID, "peer")
	if err != nil {
		t.Fatal(err)
	}
	if status.ReadAt != nil || status.NotifiedAt != nil {
		t.Fatalf("the refused message reads %+v, want it unread and not notified", status)
	}

	workingOn(t, app, run, session, "carry on")
	run.Reply("Done. <!-- attn:state=waiting_input -->")
	if got := run.Prompted(); !strings.Contains(got, inboxDoorbell) || !run.ThroughMod {
		t.Fatalf("the agent took %q (through the mod: %t), want the ring through the mod", got, run.ThroughMod)
	}
}

func TestAfterEscAnAnnotationIsTypedOnce(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session, run := moddedClaude(t, w, app)
	workingOn(t, app, run, session, "write the essay")
	stoppedByEsc(t, app, run, session)

	delivered(t, app, session, "check the rounding")
	modEvent(t, run, fakeagent.ModEventRefusedStop, "check the rounding")
	typedOnce(t, app, run, session, "check the rounding")
}

func TestAfterEscAnAskAnswerWaitsForTheUsersNextPrompt(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "asks_enabled", "true")
	session, run := moddedClaude(t, w, app)
	workingOn(t, app, run, session, "write the essay")
	stoppedByEsc(t, app, run, session)

	cacheAskAnswered(t, w, app, session)
	modEvent(t, run, fakeagent.ModEventRefusedStop, cacheAnswerText)

	workingOn(t, app, run, session, "carry on")
	modEvent(t, run, fakeagent.ModEventHeld, cacheAnswerText)
	if appended := run.ModToolBoundary(); len(appended) != 1 || !strings.Contains(appended[0], cacheAnswerText) {
		t.Fatalf("the tool boundary appended %v, want the ask's answer", appended)
	}
}

func heldRingAndAnnotation(t *testing.T, w *world, app *testworld.Peer, run *fakeagent.Run, session string) {
	t.Helper()
	workingOn(t, app, run, session, "write the essay")
	delivered(t, app, session, "check the rounding")
	modEvent(t, run, fakeagent.ModEventHeld, "check the rounding")
	cacheAskAnswered(t, w, app, session)
	modEvent(t, run, fakeagent.ModEventHeld, cacheAnswerText)
}

func TestEscReturnsAHeldRingToUnreadAndTypesAHeldAnnotationOnce(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "asks_enabled", "true")
	session, run := moddedClaude(t, w, app)
	heldRingAndAnnotation(t, w, app, run, session)

	if returned := run.ModEsc(); len(returned) != 2 {
		t.Fatalf("Esc returned %v, want the ring and the annotation", returned)
	}
	modEvent(t, run, fakeagent.ModEventRefusedStop, "check the rounding")
	typedOnce(t, app, run, session, "check the rounding")
	run.Reply("Done. <!-- attn:state=waiting_input -->")
	if got := run.Prompted(); !strings.Contains(got, cacheAnswerText) || !run.ThroughMod {
		t.Fatalf("the agent took %q (through the mod: %t), want the returned answer through the mod", got, run.ThroughMod)
	}
}

func TestLosingTheModReturnsWhatItHeld(t *testing.T) {
	tests := []struct {
		name       string
		lose       func(w *world, run *fakeagent.Run)
		throughMod bool
	}{
		{"a reload", func(w *world, run *fakeagent.Run) { run.ModReload() }, true},
		{"a mod that stops polling", func(w *world, run *fakeagent.Run) {
			run.ModStopPolling()
			keepPassingTheReachWindow(w)
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app := w.App()
			setSetting(t, app, "asks_enabled", "true")
			session, run := moddedClaude(t, w, app)
			heldRingAndAnnotation(t, w, app, run, session)

			tt.lose(w, run)

			ring := run.Prompted()
			if !strings.Contains(ring, cacheAnswerText) || run.ThroughMod != tt.throughMod {
				t.Fatalf("the agent took %q (through the mod: %t), want the ask's answer (through the mod: %t)", ring, run.ThroughMod, tt.throughMod)
			}
			run.Reply("Done. <!-- attn:state=waiting_input -->")
			if got := run.Prompted(); got != "check the rounding" || run.ThroughMod != tt.throughMod {
				t.Fatalf("the agent took %q (through the mod: %t), want the annotation (through the mod: %t)", got, run.ThroughMod, tt.throughMod)
			}
			nothingElsePrompted(t, app, run, session)
		})
	}
}

func TestHeldAnnotationsReturnInTheOrderTheModAcceptedThem(t *testing.T) {
	tests := []struct {
		name string
		lose func(w *world, run *fakeagent.Run)
	}{
		{"a reload", func(w *world, run *fakeagent.Run) { run.ModReload() }},
		{"a mod that stops polling", func(w *world, run *fakeagent.Run) {
			run.ModStopPolling()
			keepPassingTheReachWindow(w)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app := w.App()
			session, run := moddedClaude(t, w, app)
			workingOn(t, app, run, session, "write the essay")
			annotations := []string{"annotation-1", "annotation-2", "annotation-3", "annotation-4", "annotation-5", "annotation-6"}
			for _, annotation := range annotations {
				delivered(t, app, session, annotation)
				modEvent(t, run, fakeagent.ModEventHeld, annotation)
			}

			tt.lose(w, run)

			var took []string
			for len(took) < len(annotations) {
				run.Reply("Done. <!-- attn:state=waiting_input -->")
				took = append(took, strings.Split(run.Prompted(), "\n\n")...)
			}
			if !slices.Equal(took, annotations) {
				t.Fatalf("the agent took %v, want %v", took, annotations)
			}
		})
	}
}

func TestAReturnedAnnotationNeverSubmitsAnUnsentDraft(t *testing.T) {
	for name, keys := range map[string][]string{
		"typed text":                 {"unsent draft"},
		"Shift+Enter after the text": {"unsent draft", "\n"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app := w.App()
			session, run := moddedClaude(t, w, app)
			workingOn(t, app, run, session, "write the essay")
			delivered(t, app, session, "check the rounding")
			modEvent(t, run, fakeagent.ModEventHeld, "check the rounding")

			terminal := app.Terminal(session)
			app.Watch(session)
			for _, key := range keys {
				app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: key})
			}
			app.AwaitTerminalScreen(terminal, "unsent draft")

			run.ModStopPolling()
			keepPassingTheReachWindow(w)
			waited := w.PauseAt(pausepoint.ClaudeModReturnWaitsForDraft)
			run.Reply("Done. <!-- attn:state=waiting_input -->")
			waited.Await()
			waited.Release()

			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: "\r"})
			if got := strings.TrimSpace(run.Prompted()); got != "unsent draft" {
				t.Fatalf("the agent took %q, want the user's draft alone when the user sent it", got)
			}
			run.Reply("Done. <!-- attn:state=waiting_input -->")
			if got := run.Prompted(); got != "check the rounding" {
				t.Fatalf("the agent took %q, want the returned annotation after the user's turn", got)
			}
		})
	}
}

func TestAHeldAnnotationSurvivesClaudeExitingWhileItIsTyped(t *testing.T) {
	daemon.UseShippedPasteGap(t)
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session, run := moddedClaude(t, w, app)
	workingOn(t, app, run, session, "write the essay")
	delivered(t, app, session, "check the rounding")
	modEvent(t, run, fakeagent.ModEventHeld, "check the rounding")

	pasteGap := w.PauseAt(pausepoint.SessionInputPasteGap)
	run.ModStopPolling()
	keepPassingTheReachWindow(w)
	run.Reply("Done. <!-- attn:state=waiting_input -->")
	pasteGap.Await()
	run.Exit(1)
	pasteGap.Release()
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })

	launchIntentReload(t, app, session)
	if got := w.Launched(session).Prompted(); got != "check the rounding" {
		t.Fatalf("the replacement took %q, want the annotation the mod had accepted", got)
	}
}

func keepPassingTheReachWindow(w *world) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, armed := w.clock.AwaitTimer(ctx, daemon.ClaudeModReachTimer); !armed {
				return
			}
			w.clock.Advance(claudeModReachWindow)
		}
	}()
	w.T.Cleanup(func() {
		cancel()
		<-done
	})
}

func TestWithTheModNotPollingAMessageIsTyped(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session, run := moddedClaude(t, w, app)
	run.ModStopPolling()
	w.awaitTimer(daemon.ClaudeModReachTimer)
	w.advance(claudeModReachWindow)

	delivered(t, app, session, "check the rounding")
	typedOnce(t, app, run, session, "check the rounding")
}

func TestAMessageWaitingWhenTheModIsLostFailsLoudlyAndTheNextIsTyped(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session, run := moddedClaude(t, w, app)
	run.ModStopPolling()
	w.awaitTimer(daemon.ClaudeModReachTimer)

	waiting := sendFeedback(app, session, "check the rounding")
	w.awaitTimer(daemon.ClaudeModAckTimer)
	w.advance(claudeModReachWindow)
	if result := feedbackResult(app, waiting); result.Success || !strings.Contains(protocol.Deref(result.Error), "reach_window=5s") {
		t.Fatalf("the feedback was answered %+v, want a failure naming the reach window", result)
	}

	delivered(t, app, session, "check it again")
	typedOnce(t, app, run, session, "check it again")
}

func TestASecondPollForATerminalReplacesTheFirst(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session, run := moddedClaude(t, w, app)
	socket, terminal := runEnv(run, "ATTN_CLAUDE_MOD_SOCKET"), runEnv(run, "ATTN_TERMINAL_ID")
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type message struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	taken := make(chan message, 1)
	go func() {
		query := url.Values{"terminal": {terminal}, "instance": {"second"}}.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://attn/claude/next?"+query, nil)
		if err != nil {
			return
		}
		response, err := client.Do(request)
		if err != nil {
			return
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		var got message
		_ = json.Unmarshal(body, &got)
		taken <- got
	}()

	if event := run.ModNext(); event.Kind != fakeagent.ModEventReplaced {
		t.Fatalf("the first poll reported %+v, want it replaced", event)
	}
	submitted := sendFeedback(app, session, "check the rounding")
	got := <-taken
	if got.Text != "check the rounding" {
		t.Fatalf("the newer poll took %+v, want the annotation", got)
	}
	ack, _ := json.Marshal(map[string]string{"terminal": terminal, "instance": "second", "id": got.ID, "outcome": "submitted"})
	response, err := client.Post("http://attn/claude/ack", "application/json", bytes.NewReader(ack))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if result := feedbackResult(app, submitted); !result.Success || result.Status != "delivered" {
		t.Fatalf("the feedback was answered %+v, want it delivered", result)
	}
}

func TestTheClaudeModSettingDecidesTheLaunchAndTheDelivery(t *testing.T) {
	for _, launch := range []struct{ name, direct string }{{"through attn -s", "false"}, {"direct", "true"}} {
		t.Run(launch.name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app := w.App()
			setSetting(t, app, "direct_agent_launch_enabled", launch.direct)

			off := w.Launched(w.Spawn(app, fakeagent.Claude, w.Path("off")))
			if slices.Contains(off.Argv, "--plugin-dir") || runEnv(off, "ATTN_CLAUDE_MOD_SOCKET") != "" {
				t.Errorf("with the setting off the agent launched with %v, socket %q", off.Argv, runEnv(off, "ATTN_CLAUDE_MOD_SOCKET"))
			}
			delivered(t, app, off.SessionID, "typed, not modded")
			typedOnce(t, app, off, off.SessionID, "typed, not modded")

			_, on := moddedClaude(t, w, app)
			at := slices.Index(on.Argv, "--plugin-dir")
			if at < 0 || runEnv(on, "ATTN_CLAUDE_MOD_SOCKET") == "" {
				t.Fatalf("with the setting on the agent launched with %v, socket %q", on.Argv, runEnv(on, "ATTN_CLAUDE_MOD_SOCKET"))
			}
			folder := on.Argv[at+1]
			module := filepath.Join(folder, "hooks", "register.ts")
			for _, file := range []string{filepath.Join(folder, ".claude-plugin", "plugin.json"), filepath.Join(folder, "hooks", "hooks.json"), module} {
				if _, err := os.Stat(file); err != nil {
					t.Errorf("the mod folder lacks %s: %v", file, err)
				}
			}
			before, err := os.Stat(module)
			if err != nil {
				t.Fatal(err)
			}
			w.Launched(w.Spawn(app, fakeagent.Claude, w.Path("again")))
			after, err := os.Stat(module)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
				t.Errorf("another launch rewrote the unchanged module, which would reload the mod in live sessions")
			}
		})
	}
}

func runEnv(run *fakeagent.Run, name string) string {
	for _, entry := range run.Env {
		if value, ok := strings.CutPrefix(entry, name+"="); ok {
			return value
		}
	}
	return ""
}
