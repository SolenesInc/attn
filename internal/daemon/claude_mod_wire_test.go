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
	claudeModPromptLine  = "› Prompt from the attn plugin"
	claudeModReachWindow = 5 * time.Second
)

func moddedClaude(t *testing.T, w *world, app *testworld.Peer) (string, *fakeagent.Run) {
	t.Helper()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	run := w.Launched(session)
	w.awaitTimer(daemon.ClaudeModPollHoldTimer)
	return session, run
}

func typedClaude(w *world, run *fakeagent.Run) {
	w.T.Helper()
	w.awaitTimer(daemon.ClaudeModPollHoldTimer)
	run.ModStopPolling()
	w.awaitTimer(daemon.ClaudeModReachTimer)
	w.advance(claudeModReachWindow)
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

func TestAReturnedAnnotationIsSubmittedWithTheDraftInTheComposer(t *testing.T) {
	for draftName, keys := range map[string][]string{
		"typed text":                 {"unsent draft"},
		"Shift+Enter after the text": {"unsent draft", "\n"},
	} {
		for returnName, returnIt := range map[string]func(t *testing.T, w *world, app *testworld.Peer, run *fakeagent.Run, session string){
			"when the mod is lost": func(t *testing.T, w *world, app *testworld.Peer, run *fakeagent.Run, session string) {
				run.ModStopPolling()
				keepPassingTheReachWindow(w)
				run.Reply("Done. <!-- attn:state=waiting_input -->")
			},
			"at Esc": func(t *testing.T, w *world, app *testworld.Peer, run *fakeagent.Run, session string) {
				stoppedByEsc(t, app, run, session)
			},
		} {
			t.Run(draftName+" "+returnName, func(t *testing.T) {
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

				returnIt(t, w, app, run, session)

				got := run.Prompted()
				if !strings.Contains(got, "unsent draft") || !strings.Contains(got, "check the rounding") {
					t.Fatalf("the agent took %q, want the returned annotation submitted with the draft", got)
				}
			})
		}
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

type modSocket struct {
	t        *testing.T
	ctx      context.Context
	client   *http.Client
	terminal string
}

type modPollAnswer struct {
	status int
	ID     string `json:"id"`
	Text   string `json:"text"`
}

func modSocketOf(t *testing.T, run *fakeagent.Run) *modSocket {
	t.Helper()
	socket := runEnv(run, "ATTN_CLAUDE_MOD_SOCKET")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &modSocket{t: t, ctx: ctx, terminal: runEnv(run, "ATTN_TERMINAL_ID"), client: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}}
}

func (m *modSocket) poll(instance string) <-chan modPollAnswer {
	answered := make(chan modPollAnswer, 1)
	go func() {
		query := url.Values{"terminal": {m.terminal}, "instance": {instance}}.Encode()
		request, err := http.NewRequestWithContext(m.ctx, http.MethodGet, "http://attn/claude/next?"+query, nil)
		if err != nil {
			return
		}
		response, err := m.client.Do(request)
		if err != nil {
			return
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		answer := modPollAnswer{status: response.StatusCode}
		_ = json.Unmarshal(body, &answer)
		answered <- answer
	}()
	return answered
}

func (m *modSocket) ackSubmitted(instance, id string) {
	m.t.Helper()
	m.ack(instance, id, "submitted")
}

func (m *modSocket) ack(instance, id, outcome string) {
	m.t.Helper()
	ack, _ := json.Marshal(map[string]string{"terminal": m.terminal, "instance": instance, "id": id, "outcome": outcome})
	response, err := m.client.Post("http://attn/claude/ack", "application/json", bytes.NewReader(ack))
	if err != nil {
		m.t.Fatal(err)
	}
	response.Body.Close()
}

func TestASecondPollForATerminalReplacesTheFirst(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session, run := moddedClaude(t, w, app)
	mod := modSocketOf(t, run)
	taken := mod.poll("second")

	if event := run.ModNext(); event.Kind != fakeagent.ModEventReplaced {
		t.Fatalf("the first poll reported %+v, want it replaced", event)
	}
	submitted := sendFeedback(app, session, "check the rounding")
	got := <-taken
	if got.Text != "check the rounding" {
		t.Fatalf("the newer poll took %+v, want the annotation", got)
	}
	mod.ackSubmitted("second", got.ID)
	if result := feedbackResult(app, submitted); !result.Success || result.Status != "delivered" {
		t.Fatalf("the feedback was answered %+v, want it delivered", result)
	}
}

func TestAReplacedModCannotTakeItsTerminalBackWithALatePoll(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session, run := moddedClaude(t, w, app)
	mod := modSocketOf(t, run)
	before := mod.poll("before-the-reload")
	if event := run.ModNext(); event.Kind != fakeagent.ModEventReplaced {
		t.Fatalf("the fake mod's poll reported %+v, want it replaced", event)
	}
	reloaded := mod.poll("after-the-reload")
	if answer := <-before; answer.status != http.StatusConflict {
		t.Fatalf("the poll from before the reload was answered %+v, want it replaced", answer)
	}

	select {
	case answer := <-mod.poll("before-the-reload"):
		if answer.status != http.StatusConflict {
			t.Fatalf("the late poll was answered %+v, want it refused", answer)
		}
	case answer := <-reloaded:
		t.Fatalf("the late poll took the terminal: the reloaded mod's poll was answered %+v", answer)
	}

	submitted := sendFeedback(app, session, "check the rounding")
	got := <-reloaded
	if got.Text != "check the rounding" {
		t.Fatalf("the reloaded mod took %+v, want the annotation", got)
	}
	mod.ackSubmitted("after-the-reload", got.ID)
	if result := feedbackResult(app, submitted); !result.Success || result.Status != "delivered" {
		t.Fatalf("the feedback was answered %+v, want it delivered", result)
	}
}

func TestAClaudeLaunchLoadsTheModFromTheDaemonsFolder(t *testing.T) {
	t.Setenv("ATTN_CLAUDE_MOD_SOCKET", "/inherited/claude-mod.sock")
	for _, launch := range []struct{ name, direct string }{{"through attn -s", "false"}, {"direct", "true"}} {
		t.Run(launch.name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app := w.App()
			setSetting(t, app, "direct_agent_launch_enabled", launch.direct)

			_, on := moddedClaude(t, w, app)
			at := slices.Index(on.Argv, "--plugin-dir")
			if at < 0 || runEnv(on, "ATTN_CLAUDE_MOD_SOCKET") == "" {
				t.Fatalf("the agent launched with %v, socket %q", on.Argv, runEnv(on, "ATTN_CLAUDE_MOD_SOCKET"))
			}
			folder := on.Argv[at+1]
			module := filepath.Join(folder, "hooks", "register.tsx")
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

func permissionQuestion(t *testing.T, w *world, app *testworld.Peer, session string) {
	t.Helper()
	if err := w.Client().RecordNotification(protocol.TerminalID(w.Terminal(session)), "permission_prompt", "Allow edit?"); err != nil {
		t.Fatalf("ask for approval: %v", err)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
}

func TestAMessageDuringATurnsPermissionQuestionWaitsInTheMod(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session, run := moddedClaude(t, w, app)
	workingOn(t, app, run, session, "write the essay")
	permissionQuestion(t, w, app, session)

	delivered(t, app, session, "check the rounding")
	modEvent(t, run, fakeagent.ModEventHeld, "check the rounding")
	if appended := run.ModToolBoundary(); len(appended) != 1 || appended[0] != "check the rounding" {
		t.Fatalf("the tool boundary appended %v, want the annotation", appended)
	}
}

func TestAQuestionOutsideATurnKeepsAMessageFromTheMod(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session, run := moddedClaude(t, w, app)
	app.AwaitScreen(session, "? for shortcuts")
	permissionQuestion(t, w, app, session)

	if got := submitSessionAnnotationFeedback(app, session, "check the rounding"); got.status != "skipped_pending_approval" {
		t.Fatalf("the annotation was answered %+v, want it kept for a retry", got)
	}
	if appended := run.ModToolBoundary(); len(appended) != 0 {
		t.Fatalf("the mod took %v", appended)
	}
}

func TestAMessageDuringATurnsQuestionIsNotTypedWithoutTheMod(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session, run := moddedClaude(t, w, app)
	workingOn(t, app, run, session, "write the essay")
	run.ModStopPolling()
	w.awaitTimer(daemon.ClaudeModReachTimer)
	w.advance(claudeModReachWindow)
	permissionQuestion(t, w, app, session)

	if got := submitSessionAnnotationFeedback(app, session, "check the rounding"); got.status != "skipped_pending_approval" {
		t.Fatalf("the annotation was answered %+v, want it kept for a retry, never typed into the question", got)
	}
}

func TestAMenuKeyInAQuestionDoesNotHoldBackAReturnedAnnotation(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session, run := moddedClaude(t, w, app)
	workingOn(t, app, run, session, "write the essay")
	permissionQuestion(t, w, app, session)
	delivered(t, app, session, "check the rounding")
	modEvent(t, run, fakeagent.ModEventHeld, "check the rounding")

	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(app.Terminal(session)), Data: "\x1b[B"})
	if err := w.Client().UpdateState(protocol.TerminalID(w.Terminal(session)), protocol.StateWorking); err != nil {
		t.Fatalf("approve: %v", err)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	stoppedByEsc(t, app, run, session)

	modEvent(t, run, fakeagent.ModEventRefusedStop, "check the rounding")
	typedOnce(t, app, run, session, "check the rounding")
}

func TestAnAnswerDuringAUsersPermissionQuestionKeepsTheirTurnOwedAcrossARestart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer bool
	}{
		{"with no answer", false},
		{"with an answer during the question", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app := w.App()
			setSetting(t, app, "queue_mode_enabled", "true")
			setSetting(t, app, "asks_enabled", "true")
			session, run := moddedClaude(t, w, app)
			app.TypeLine(session, "find why the build fails")
			run.Prompted()
			working := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			permissionQuestion(t, w, app, session)

			if tc.answer {
				cacheAskAnswered(t, w, app, session)
				modEvent(t, run, fakeagent.ModEventHeld, cacheAnswerText)
			}
			if err := w.Client().UpdateState(protocol.TerminalID(w.Terminal(session)), protocol.StateWorking); err != nil {
				t.Fatalf("approve: %v", err)
			}
			resumed := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			if tc.answer {
				if appended := run.ModToolBoundary(); len(appended) != 1 || !strings.Contains(appended[0], cacheAnswerText) {
					t.Fatalf("the tool boundary appended %v, want the answer", appended)
				}
			}
			run.Reply("an invalid flag <!-- attn:state=waiting_input -->")
			stopped := testworld.AwaitStateAfter(app, resumed, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
			if !protocol.Deref(stopped.TurnOwed) || protocol.Deref(stopped.TurnByAgent) {
				t.Fatalf("the user's run stopped with owed=%t by_agent=%t, want their turn owed", protocol.Deref(stopped.TurnOwed), protocol.Deref(stopped.TurnByAgent))
			}

			w.restart()
			after := queriedSession(t, w.Client(), session)
			if !protocol.Deref(after.TurnOwed) || protocol.Deref(after.TurnByAgent) {
				t.Fatalf("after a restart owed=%t by_agent=%t, want their turn owed", protocol.Deref(after.TurnOwed), protocol.Deref(after.TurnByAgent))
			}
		})
	}
}

func TestAnAnswerThatStartsARunThroughTheModStaysOutOfTheQueue(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "queue_mode_enabled", "true")
	setSetting(t, app, "asks_enabled", "true")
	session, run := moddedClaude(t, w, app)
	idle := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State != protocol.SessionStateLaunching })

	cacheAskAnswered(t, w, app, session)
	if got := run.Prompted(); !strings.Contains(got, cacheAnswerText) {
		t.Fatalf("the agent took %q, want the answer", got)
	}
	working := testworld.AwaitStateAfter(app, idle, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	run.Reply("noted <!-- attn:state=waiting_input -->")
	stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	if protocol.Deref(stopped.TurnOwed) {
		t.Fatal("a run the answer started opened a turn with quiet notifications on")
	}
}

func TestAQuickCaptureThroughTheModOpensTheUsersTurn(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.TrustedApp(), w.Client()
	setSetting(t, app, "queue_mode_enabled", "true")
	registerSessions(t, w, cli, "reviewer")
	session, run := moddedClaude(t, w, app)
	idle := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State != protocol.SessionStateLaunching })

	sendQuickCapture(t, app, sessionMailbox(session), "what is the review status?")
	if got := run.Prompted(); !strings.Contains(got, "what is the review status?") {
		t.Fatalf("the agent took %q, want the user's question", got)
	}
	working := testworld.AwaitStateAfter(app, idle, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	run.Reply("The review is green. <!-- attn:state=waiting_input -->")
	answered := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	if !protocol.Deref(answered.TurnOwed) || protocol.Deref(answered.TurnByAgent) {
		t.Fatalf("the reply stopped with owed=%t by_agent=%t, want the user's turn open", protocol.Deref(answered.TurnOwed), protocol.Deref(answered.TurnByAgent))
	}

	settled := settleFromApp(app, answered)
	sendAgentMessage(t, cli, "reviewer", session, "a review arrived")
	if got := run.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("the agent took %q, want the inbox doorbell", got)
	}
	working = testworld.AwaitStateAfter(app, settled, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	sendQuickCapture(t, app, sessionMailbox(session), "what did the reviewer say?")
	modEvent(t, run, fakeagent.ModEventHeld, "what did the reviewer say?")
	run.Reply("They approved. <!-- attn:state=waiting_input -->")
	steered := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	if !protocol.Deref(steered.TurnOwed) || protocol.Deref(steered.TurnByAgent) {
		t.Fatalf("the steered run stopped with owed=%t by_agent=%t, want the user's turn open", protocol.Deref(steered.TurnOwed), protocol.Deref(steered.TurnByAgent))
	}
}

func TestARingTheModNeverTookLeavesTheUsersTurnOwedThroughAnUnrelatedRun(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "queue_mode_enabled", "true")
	registerSessions(t, w, cli, "reviewer")
	session, run := moddedClaude(t, w, app)
	idle := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State != protocol.SessionStateLaunching })
	owed := userRunStops(app, run, idle, "run a background agent", "Waiting for it. <!-- attn:state=waiting_input -->")
	if !protocol.Deref(owed.TurnOwed) || protocol.Deref(owed.TurnByAgent) {
		t.Fatalf("the user's run stopped with owed=%t by_agent=%t, want their turn owed", protocol.Deref(owed.TurnOwed), protocol.Deref(owed.TurnByAgent))
	}

	run.ModStopPolling()
	w.awaitTimer(daemon.ClaudeModReachTimer)
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		sendAgentMessage(t, cli, "reviewer", session, "a review arrived")
	}()
	w.awaitTimer(daemon.ClaudeModAckTimer)
	if err := cli.UpdateState(protocol.TerminalID(w.Terminal(session)), protocol.StateWorking); err != nil {
		t.Fatalf("the background agent finishes: %v", err)
	}
	working := testworld.AwaitStateAfter(app, owed, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	w.advance(claudeModReachWindow)
	<-returned

	run.Reply("Background work is done. <!-- attn:state=waiting_input -->")
	stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	w.restart()
	after := queriedSession(t, w.Client(), session)
	for name, s := range map[string]protocol.Session{"the unrelated run": working, "its stop": stopped, "a restart": after} {
		if !protocol.Deref(s.TurnOwed) || protocol.Deref(s.TurnByAgent) {
			t.Errorf("at %s owed=%t by_agent=%t, want the user's turn owed", name, protocol.Deref(s.TurnOwed), protocol.Deref(s.TurnByAgent))
		}
	}
}

func TestARingThroughTheModToAWaitingAgentSettlesItsTurnAndStaysOutOfTheQueue(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "queue_mode_enabled", "true")
	registerSessions(t, w, cli, "reviewer")
	session, run := moddedClaude(t, w, app)
	idle := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State != protocol.SessionStateLaunching })
	owed := userRunStops(app, run, idle, "look at the build", "Done. <!-- attn:state=waiting_input -->")

	sendAgentMessage(t, cli, "reviewer", session, "a review arrived")
	if got := run.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("the agent took %q, want the inbox doorbell", got)
	}
	working := testworld.AwaitStateAfter(app, owed, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return !protocol.Deref(s.TurnOwed) && protocol.Deref(s.TurnByAgent) })
	run.Reply("Read it. <!-- attn:state=waiting_input -->")
	stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	if protocol.Deref(stopped.TurnOwed) {
		t.Fatal("a run the ring started opened a turn with quiet notifications on")
	}
}

func TestTheAppSeesWhatAReturnedRingCarriedAsWaitingUntilTheNextRingPlacesIt(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.TrustedApp()
	session, run := moddedClaude(t, w, app)
	cli := w.Client()
	seed := plantDelegationSeed(t, cli, session, "Search endpoint")
	moveDelegationSeed(t, cli, session, seed, "tend", "")
	workingOn(t, app, run, session, "write the essay")

	capture := uuid.NewString()
	if _, err := app.QuickCapture(protocol.QuickCaptureSendMessage{
		Cmd: protocol.CmdQuickCaptureSend, CaptureID: capture, Content: "check the rounding", AttachmentIds: []string{},
		Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(protocol.SessionID(session))},
	}); err != nil {
		t.Fatal(err)
	}
	modEvent(t, run, fakeagent.ModEventHeld, "check the rounding")
	testworld.Await(app, protocol.EventQuickCaptureRead, func(m protocol.QuickCaptureReadMessage) bool { return m.CaptureID == capture })

	testworld.Request(app, protocol.SeedAuthorizationMessage{
		Cmd: protocol.CmdSeedAuthorization, RequestID: protocol.Ptr("set"), SeedID: seed, Text: protocol.Ptr("You may land on main."),
	}, protocol.EventSeedAuthorizationResult, func(m protocol.SeedAuthorizationResultMessage) bool { return m.RequestID == "set" })
	modEvent(t, run, fakeagent.ModEventHeld, "You may land on main.")
	typedAt := func(m protocol.GardenSeedsChangedMessage) (typed, waiting bool) {
		for _, changed := range m.Seeds {
			if changed.ID == seed && changed.Authorization != nil && changed.Authorization.Update != nil {
				typed = changed.Authorization.Update.TypedAt != nil
				return typed, !typed
			}
		}
		return false, false
	}
	testworld.Await(app, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
		typed, _ := typedAt(m)
		return typed
	})

	mark := len(app.Log())
	stoppedByEsc(t, app, run, session)
	testworld.AwaitSince(app, mark, protocol.EventQuickCaptureUnread, func(m protocol.QuickCaptureUnreadMessage) bool { return m.CaptureID == capture })
	testworld.AwaitSince(app, mark, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
		_, waiting := typedAt(m)
		return waiting
	})
	if r := quickCaptureRecord(t, app, capture); r.ReadAt != nil {
		t.Fatalf("the history lists the returned capture as read at %s", *r.ReadAt)
	}

	mark = len(app.Log())
	run.Input("carry on")
	if got := run.Prompted(); got != "carry on" {
		t.Fatalf("the agent took %q, want the user's prompt", got)
	}
	modEvent(t, run, fakeagent.ModEventHeld, "check the rounding")
	testworld.AwaitSince(app, mark, protocol.EventQuickCaptureRead, func(m protocol.QuickCaptureReadMessage) bool { return m.CaptureID == capture })
	testworld.AwaitSince(app, mark, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
		typed, _ := typedAt(m)
		return typed
	})
}
