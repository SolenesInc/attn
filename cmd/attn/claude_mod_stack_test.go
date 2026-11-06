package main_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/fakeclaudeapi"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAgentMessageReachesRealClaudeThroughTheMod(t *testing.T) {
	t.Parallel()
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not on PATH")
	}
	model := fakeclaudeapi.Start(t)
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	work := s.Path("shop")
	configDir := filepath.Join(s.Dir, "toolhome", ".claude")
	writeClaudeConfig(t, claude, configDir, work)
	s.Vars = append(s.Vars,
		"ATTN_CLAUDE_EXECUTABLE="+claude,
		"CLAUDE_CONFIG_DIR="+configDir,
		"ANTHROPIC_BASE_URL="+model.URL,
		"ANTHROPIC_API_KEY="+fakeclaudeapi.APIKey,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"DISABLE_AUTOUPDATER=1",
	)
	s.Start()
	s.Attn("settings", "set", "claude_mod_enabled", "true")
	const reviewer = "rev-1111-2222"
	register(t, s, reviewer, "reviewer")

	app := s.App()
	recipient := s.Spawn(app, fakeagent.Claude, work)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("recipient screen:\n%s", s.Attn("agent", "peek", recipient).Stdout)
		}
	})
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)

	mark := len(app.Log())
	if sent := s.Run(testworld.Invocation{Args: []string{"agent", "msg", recipient, "the discount is applied after tax"}, Session: reviewer}); sent.Code != 0 {
		t.Fatalf("attn agent msg exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
	}
	const ring = "attn agent inbox"
	model.AwaitPrompt(t, ring)
	working := testworld.AwaitSessionSince(app, mark, recipient, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })

	rows := claudeUserRows(t, configDir)
	for _, row := range rows {
		if strings.Contains(row.Text, ring) {
			if row.Origin.Kind != "plugin" || row.Origin.Name != "attn" {
				t.Fatalf("Claude recorded the ring with origin %+v, want the attn plugin's; it was typed, not handed to the mod", row.Origin)
			}
			return
		}
	}
	t.Fatalf("Claude's transcript has no user row with %q; rows: %+v", ring, rows)
}

func TestRefusingAPermissionQuestionLeavesRealClaudeFreeToTakeMessages(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{"with No", "2"},
		{"with Esc", "\x1b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			claude, err := exec.LookPath("claude")
			if err != nil {
				t.Skip("claude is not on PATH")
			}
			model := fakeclaudeapi.Start(t)
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			work := s.Path("shop")
			configDir := filepath.Join(s.Dir, "toolhome", ".claude")
			writeClaudeConfig(t, claude, configDir, work)
			settings := fmt.Sprintf(`{"permissions":{"ask":["Bash"]},"hooks":{"Notification":[{"matcher":"permission_prompt","hooks":[{"type":"command","command":"curl -s %s%s"}]}]}}`, model.URL, fakeclaudeapi.QuestionWaitingPath)
			if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(settings), 0o600); err != nil {
				t.Fatal(err)
			}
			s.Vars = append(s.Vars,
				"ATTN_CLAUDE_EXECUTABLE="+claude,
				"CLAUDE_CONFIG_DIR="+configDir,
				"ANTHROPIC_BASE_URL="+model.URL,
				"ANTHROPIC_API_KEY="+fakeclaudeapi.APIKey,
				"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
				"DISABLE_AUTOUPDATER=1",
			)
			s.Start()
			s.Attn("settings", "set", "claude_mod_enabled", "true")
			const reviewer = "rev-1111-2222"
			register(t, s, reviewer, "reviewer")

			app := s.App()
			recipient := s.Spawn(app, fakeagent.Claude, work)
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("recipient screen:\n%s", s.Attn("agent", "peek", recipient).Stdout)
				}
			})
			s.AwaitTimer(daemon.ClaudeModPollHoldTimer)

			app.TypeLine(recipient, fakeclaudeapi.ToolPrompt)
			testworld.AwaitSession(app, recipient, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
			const held = "held through the question"
			app.Send(protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "held", SessionID: protocol.SessionID(recipient), Text: held})
			if taken := testworld.Await(app, protocol.EventSessionAnnotationsSubmitResult, func(r protocol.SessionAnnotationsSubmitResultMessage) bool {
				return r.RequestID == "held"
			}); taken.Status != "delivered" {
				t.Fatalf("the annotation was answered %+v, want it taken during the question", taken)
			}
			model.AwaitPrompt(t, fakeclaudeapi.QuestionWaitingPath)
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(app.Terminal(recipient)), Data: tc.key})
			model.AwaitPrompt(t, held)

			const later = "the discount is applied after tax"
			if sent := s.Run(testworld.Invocation{Args: []string{"agent", "msg", recipient, later}, Session: reviewer}); sent.Code != 0 {
				t.Fatalf("attn agent msg exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
			}
			model.AwaitPrompt(t, "attn agent inbox")
		})
	}
}

type claudeUserRow struct {
	Text   string
	Origin struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
}

func claudeUserRows(t *testing.T, configDir string) []claudeUserRow {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(configDir, "projects", "*", "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("find Claude's transcript under %s: %v (found %d)", configDir, err, len(files))
	}
	var rows []claudeUserRow
	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		lines := bufio.NewScanner(f)
		lines.Buffer(nil, 16<<20)
		for lines.Scan() {
			var entry struct {
				Type    string          `json:"type"`
				Origin  json.RawMessage `json:"origin"`
				Message struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(lines.Bytes(), &entry) != nil || entry.Type != "user" {
				continue
			}
			var row claudeUserRow
			row.Text = fakeclaudeapi.ContentText(entry.Message.Content)
			if len(entry.Origin) > 0 {
				_ = json.Unmarshal(entry.Origin, &row.Origin)
			}
			rows = append(rows, row)
		}
		f.Close()
	}
	return rows
}

const enablePromptSuggestions = "CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=true"

func TestAgentPeekLabelsARealClaudeSuggestionAndNotATypedDraft(t *testing.T) {
	t.Parallel()
	s, app, session, model, _ := realModdedClaude(t, enablePromptSuggestions)
	requirePeekLabelsOnlyTheSuggestion(t, s, app, session, model)
}

func TestAgentPeekFromHomeLabelsARealClaudeSuggestionOnAnOutpostAndNotATypedDraft(t *testing.T) {
	t.Parallel()
	c := newRealOutpostClaude(t, false, enablePromptSuggestions)
	requirePeekLabelsOnlyTheSuggestion(t, c.home, c.pair.App, c.session, c.model)
}

func requirePeekLabelsOnlyTheSuggestion(t *testing.T, s *testworld.Stack, app *testworld.Peer, session string, model *fakeclaudeapi.Model) {
	t.Helper()
	app.Watch(session)

	release := model.HoldSuggestion("keep the gate, harvest-it")
	defer release()
	for _, prompt := range []string{"land-the-checkout-fix", "what-is-left-now"} {
		mark := len(app.Log())
		modelMark := model.Mark()
		app.Type(session, prompt, "keyboard")
		app.AwaitScreen(session, prompt)
		app.Type(session, "\r", "keyboard")
		model.AwaitPromptSince(t, modelMark, prompt)
		working := testworld.AwaitSessionSince(app, mark, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
	}
	model.AwaitPrompt(t, fakeclaudeapi.SuggestionPrompt)
	release()
	app.AwaitScreen(session, "harvest-it")
	if !peekSaysSuggestion(t, s, session) {
		t.Errorf("peek does not label Claude's suggestion:\n%s", s.Attn("agent", "peek", session).Stdout)
	}

	const draft = "discount-field-first"
	app.Type(session, draft, "keyboard")
	app.AwaitScreen(session, draft)
	if peekSaysSuggestion(t, s, session) {
		t.Errorf("peek labels a typed draft as a suggestion:\n%s", s.Attn("agent", "peek", session).Stdout)
	}
}

func writeClaudeConfig(t *testing.T, claude, configDir, work string) {
	t.Helper()
	if err := fakeclaudeapi.WriteClaudeConfig(claude, configDir, work); err != nil {
		t.Fatal(err)
	}
}

func TestMailFromHomeReachesRealClaudeOnAnOutpostThroughTheModAndIsTypedOnceTheSettingIsOff(t *testing.T) {
	t.Parallel()
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not on PATH")
	}
	model := fakeclaudeapi.Start(t)
	home, outpost := testworld.NewStack(t), testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	work := outpost.Path("shop")
	configDir := filepath.Join(outpost.Dir, "toolhome", ".claude")
	writeClaudeConfig(t, claude, configDir, work)
	outpost.Vars = append(outpost.Vars,
		"ATTN_CLAUDE_EXECUTABLE="+claude,
		"CLAUDE_CONFIG_DIR="+configDir,
		"ANTHROPIC_BASE_URL="+model.URL,
		"ANTHROPIC_API_KEY="+fakeclaudeapi.APIKey,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"DISABLE_AUTOUPDATER=1",
	)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	requireStdout(t, home.Attn("settings", "set", "claude_mod_enabled", "true"), "claude_mod_enabled = true")
	copied := outpost.App()
	pair.Connect()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	testworld.Await(copied, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["claude_mod_enabled"] == "true" })
	const reviewer = "rev-1111-2222"
	register(t, home, reviewer, "reviewer")

	app := pair.App
	recipient := home.Spawn(app, fakeagent.Claude, work, func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("recipient screen:\n%s", home.Attn("agent", "peek", recipient).Stdout)
		}
	})
	outpost.AwaitTimer(daemon.ClaudeModPollHoldTimer)

	const ring = "attn agent inbox"
	originOfRing := func(rings int, mail string) (string, string) {
		t.Helper()
		before := model.Mark()
		appMark := len(app.Log())
		if sent := home.Run(testworld.Invocation{Args: []string{"agent", "msg", recipient, mail}, Session: reviewer}); sent.Code != 0 {
			t.Fatalf("attn agent msg exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
		}
		model.AwaitPromptSince(t, before, ring)
		worked := false
		testworld.AwaitSessionSince(app, appMark, recipient, func(s protocol.Session) bool {
			worked = worked || s.State == protocol.SessionStateWorking
			return worked && s.State != protocol.SessionStateWorking
		})
		rows := claudeUserRows(t, configDir)
		var matching []claudeUserRow
		for _, row := range rows {
			if strings.Contains(row.Text, ring) {
				matching = append(matching, row)
			}
		}
		if len(matching) != rings {
			t.Fatalf("Claude's transcript has %d user rows with %q, want %d; rows: %+v", len(matching), ring, rings, rows)
		}
		last := matching[len(matching)-1]
		return last.Origin.Kind, last.Origin.Name
	}

	if kind, name := originOfRing(1, "the discount is applied after tax"); kind != "plugin" || name != "attn" {
		t.Fatalf("Claude recorded the ring with origin %s/%s, want the attn plugin's; it was typed, not handed to the mod", kind, name)
	}
	t.Log("with the setting on, Claude on the outpost recorded the ring with origin plugin/attn")

	if read := home.Run(testworld.Invocation{Args: []string{"agent", "inbox"}, Session: recipient}); read.Code != 0 || !strings.Contains(read.Stdout, "the discount is applied after tax") {
		t.Fatalf("attn agent inbox exited %d with %q%s", read.Code, read.Stdout, read.Stderr)
	}
	requireStdout(t, home.Attn("settings", "set", "claude_mod_enabled", "false"), "claude_mod_enabled = false")
	testworld.Await(copied, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["claude_mod_enabled"] == "false" })
	if kind, name := originOfRing(2, "the total is rounded last"); kind == "plugin" {
		t.Fatalf("Claude recorded the ring with origin %s/%s with the setting off, want it typed", kind, name)
	}
	t.Log("with the setting off, the same session was typed into")
}

func TestAQuickCaptureRealClaudesModHoldsThroughADaemonRestartComesBackUnreadAtEsc(t *testing.T) {
	t.Parallel()
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not on PATH")
	}
	model := fakeclaudeapi.Start(t)
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	work := s.Path("shop")
	configDir := filepath.Join(s.Dir, "toolhome", ".claude")
	writeClaudeConfig(t, claude, configDir, work)
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{"permissions":{"allow":["Bash(cat:*)"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Vars = append(s.Vars,
		"ATTN_CLAUDE_EXECUTABLE="+claude,
		"CLAUDE_CONFIG_DIR="+configDir,
		"ANTHROPIC_BASE_URL="+model.URL,
		"ANTHROPIC_API_KEY="+fakeclaudeapi.APIKey,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"DISABLE_AUTOUPDATER=1",
	)
	s.Start()
	requireStdout(t, s.Attn("settings", "set", "claude_mod_enabled", "true"), "claude_mod_enabled = true")
	app := s.TrustedApp()
	recipient := s.Spawn(app, fakeagent.Claude, work)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("recipient screen:\n%s", s.Attn("agent", "peek", recipient).Stdout)
		}
	})
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)

	const longTask = "run the long task"
	fifo := makeFifo(t, filepath.Join(work, "tool.fifo"))
	model.RunBashAfter(longTask, "cat "+fifo)
	app.TypeLine(recipient, longTask)
	opened := make(chan *os.File, 1)
	go func() {
		toolRunning, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err == nil {
			opened <- toolRunning
		}
	}()
	select {
	case toolRunning := <-opened:
		defer toolRunning.Close()
	case <-time.After(fakeagent.HangGuard):
		t.Fatalf("Claude did not start the tool within %s", fakeagent.HangGuard)
	}

	capture := uuid.NewString()
	const words = "check the rounding"
	if _, err := app.QuickCapture(protocol.QuickCaptureSendMessage{
		Cmd: protocol.CmdQuickCaptureSend, CaptureID: capture, Content: words, AttachmentIds: []string{},
		Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(protocol.SessionID(recipient))},
	}); err != nil {
		t.Fatal(err)
	}
	testworld.Await(app, protocol.EventQuickCaptureRead, func(m protocol.QuickCaptureReadMessage) bool { return m.CaptureID == capture })

	s.Stop()
	s.Start()
	app = s.TrustedApp()
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)

	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(app.Terminal(recipient)), Data: "\x1b"})
	testworld.Await(app, protocol.EventQuickCaptureUnread, func(m protocol.QuickCaptureUnreadMessage) bool { return m.CaptureID == capture })

	mark := model.Mark()
	app.TypeLine(recipient, "carry on")
	model.AwaitPromptSince(t, mark, words)
}
