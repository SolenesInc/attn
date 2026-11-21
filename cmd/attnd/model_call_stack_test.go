package main_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type modelCallAnswer struct {
	Text    *string `json:"text"`
	Harness string  `json:"harness"`
	Model   string  `json:"model"`
	ModelMs *int    `json:"model_ms"`
	Error   *string `json:"error"`
	LimitMs *int    `json:"limit_ms"`
}

func modelCallStdin(token string, request map[string]any) string {
	body := map[string]any{"purpose": "test", "system": "Summarize in one line.", "input": "the long text", "limit_ms": 20000}
	for key, value := range request {
		body[key] = value
	}
	encoded, _ := json.Marshal(body)
	return token + "\n" + string(encoded)
}

func callModel(s *testworld.Stack, token string, request map[string]any) *testworld.Running {
	return s.Launch(testworld.Invocation{Args: []string{"model", "call", "--token-stdin"}, Stdin: modelCallStdin(token, request)})
}

func answered(t *testing.T, result testworld.Result) modelCallAnswer {
	t.Helper()
	var answer modelCallAnswer
	if lines := strings.Split(strings.TrimSpace(result.Stdout), "\n"); len(lines) != 1 || json.Unmarshal([]byte(lines[0]), &answer) != nil {
		t.Fatalf("attn model call printed %q, want one JSON line (stderr %q)", result.Stdout, result.Stderr)
	}
	return answer
}

func modelCallStack(t *testing.T) (*testworld.Stack, string) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, fakeagent.Codex))
	s.Vars = append(s.Vars, "ATTN_HEADLESS_TASKS=on")
	s.Start()
	token := pairSender(t, s, "Lens")
	s.TrustedApp().Send(protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: true, IdleSeconds: protocol.Ptr(0.0)})
	return s, token
}

func TestAPairedSenderCallsAModelThroughTheSpare(t *testing.T) {
	s, token := modelCallStack(t)

	spare := s.Spare()
	if !containsRun(spare.Argv, "--disallowedTools", "*") || !containsRun(spare.Argv, "--input-format", "stream-json") ||
		slices.Contains(spare.Argv, "--model") || slices.Contains(spare.Argv, "--system-prompt") {
		t.Fatalf("the spare started as %q, want stream-json input, no tools, and no model or instructions yet", spare.Argv)
	}

	call := callModel(s, token, nil)
	task := s.HeadlessTask()
	if task.Prompt != "Summarize in one line.\n\nthe long text" || task.Model != "claude-sonnet-fake" || !slices.Equal(task.Argv, spare.Argv) {
		t.Fatalf("the call reached %+v, want the spare given the standard model and the instructions before the input", task)
	}
	if slices.ContainsFunc(task.Argv, func(arg string) bool { return strings.Contains(arg, "the long text") }) {
		t.Fatalf("the input is in the spare's arguments %q; it belongs on stdin", task.Argv)
	}
	next := s.Spare()
	task.Answer("A one-line summary.")
	result := call.Wait()
	answer := answered(t, result)
	if result.Code != 0 || protocol.Deref(answer.Text) != "A one-line summary." || answer.Harness != "claude" ||
		answer.Model != "claude-sonnet-fake" || protocol.Deref(answer.ModelMs) != fakeagent.FakeModelMs || answer.Error != nil {
		t.Fatalf("attn model call = %+v, %+v; want the answer with harness, model and model_ms", result, answer)
	}
	spare.AwaitExited()
	if next.Exited() {
		t.Fatal("the next spare ended after the call; it should wait for the next one")
	}

	for _, named := range []struct {
		request map[string]any
		model   string
	}{
		{map[string]any{"tier": "light"}, "claude-haiku-fake"},
		{map[string]any{"model": "claude-opus-fake"}, "claude-opus-fake"},
	} {
		call := callModel(s, token, named.request)
		task := s.HeadlessTask()
		if task.Model != named.model || !slices.Equal(task.Argv, next.Argv) {
			t.Fatalf("a call naming %v reached %+v, want the spare with %s", named.request, task, named.model)
		}
		taken := next
		next = s.Spare()
		task.Answer("ok")
		if answer := answered(t, call.Wait()); answer.Model != named.model {
			t.Fatalf("a call naming %v answered %+v", named.request, answer)
		}
		taken.AwaitExited()
	}

	codex := callModel(s, token, map[string]any{"harness": "codex", "tier": "light"})
	task = s.HeadlessTask()
	if task.Harness != fakeagent.Codex {
		t.Fatalf("a call naming codex reached %+v", task)
	}
	task.Answer("from codex")
	if answer := answered(t, codex.Wait()); answer.Harness != "codex" || answer.Model != task.Model || protocol.Deref(answer.Text) != "from codex" {
		t.Fatalf("a call naming codex answered %+v, want codex's light model %q", answer, task.Model)
	}
	if next.Exited() {
		t.Fatal("a cold codex call ended the claude spare")
	}

	requireStdout(t, s.Attn("settings", "set", "model_calls.model", "claude-opus-fake"), "claude-opus-fake")
	defaults := callModel(s, token, nil)
	task = s.HeadlessTask()
	if task.Model != "claude-opus-fake" {
		t.Fatalf("with model_calls.model set, a call naming nothing reached %+v", task)
	}
	next = s.Spare()
	task.Answer("ok")
	defaults.Wait()

	requireStdout(t, s.Attn("settings", "set", "model_calls.agent", "codex"), "codex")
	next.AwaitExited()
	defaults = callModel(s, token, nil)
	task = s.HeadlessTask()
	if task.Harness != fakeagent.Codex || task.Model != "claude-opus-fake" {
		t.Fatalf("with model_calls.agent codex, a call naming nothing reached %+v", task)
	}
	task.Answer("ok")
	if answer := answered(t, defaults.Wait()); answer.Harness != "codex" {
		t.Fatalf("with model_calls.agent codex, a call naming nothing answered %+v", answer)
	}
}

func TestAModelCallIsRefusedOrEndsAsItsCallerSays(t *testing.T) {
	s, token := modelCallStack(t)
	s.Spare()

	for _, refused := range []struct {
		request map[string]any
		stderr  string
	}{
		{map[string]any{"tier": "light", "model": "claude-opus-fake"}, "model call: name a tier or a model, not both"},
		{map[string]any{"tier": "huge"}, `model call: tier must be light, standard or deep, not "huge"`},
		{map[string]any{"input": ""}, "model call: input must not be empty; send the text the model answers"},
		{map[string]any{"input": " \n", "harness": "codex"}, "model call: input must not be empty; send the text the model answers"},
		{map[string]any{"harness": "gemini"}, `model call: "gemini" does not answer model calls; model calls are answered by claude or codex`},
		{map[string]any{"harness": "sigma"}, `model call: "sigma" does not answer model calls; model calls are answered by claude or codex`},
	} {
		result := s.Run(testworld.Invocation{Args: []string{"model", "call", "--token-stdin"}, Stdin: modelCallStdin(token, refused.request)})
		if result.Code != 1 || result.Stdout != "" || lastLine(result.Stderr) != refused.stderr {
			t.Errorf("a call naming %v = %+v, want exit 1 and %q", refused.request, result, refused.stderr)
		}
	}

	limited := callModel(s, token, map[string]any{"limit_ms": 25000})
	s.AwaitTimer(daemon.ModelCallLimitTimer)
	slow := s.HeadlessTask()
	s.Advance(25 * time.Second)
	result := limited.Wait()
	if answer := answered(t, result); result.Code != 1 || protocol.Deref(answer.LimitMs) != 25000 || answer.Error == nil || answer.Text != nil {
		t.Fatalf("a call past its limit = %+v, %+v; want exit 1 with error and limit_ms 25000", result, answer)
	}
	slow.AwaitExited()
	slow.Answer("too late")

	stopped := callModel(s, token, nil)
	abandoned := s.HeadlessTask()
	stopped.Kill()
	abandoned.AwaitExited()
	abandoned.Answer("never read")

	for _, stranger := range []string{"not-a-paired-token", ""} {
		sent := s.Run(testworld.Invocation{Args: []string{"send", "sessions", "--token-stdin"}, Stdin: stranger + "\n"})
		refused := s.Run(testworld.Invocation{Args: []string{"model", "call", "--token-stdin"}, Stdin: modelCallStdin(stranger, nil)})
		sendError := strings.TrimPrefix(lastLine(sent.Stderr), "send sessions: ")
		if refused.Code != sent.Code || refused.Stdout != "" || lastLine(refused.Stderr) != "model call: "+sendError {
			t.Errorf("attn model call with token %q = %+v; attn send refuses it with %+v", stranger, refused, sent)
		}
	}

	var running []*testworld.Running
	var tasks []*fakeagent.HeadlessTask
	for i := range 8 {
		running = append(running, callModel(s, token, map[string]any{"input": fmt.Sprintf("call %d", i)}))
		tasks = append(tasks, s.HeadlessTask())
	}
	ninth := s.Run(testworld.Invocation{Args: []string{"model", "call", "--token-stdin"}, Stdin: modelCallStdin(token, nil)})
	if ninth.Code != 1 || lastLine(ninth.Stderr) != "model call: 8 model calls are already running, attn's limit; retry when one answers" {
		t.Fatalf("a ninth call while eight run = %+v", ninth)
	}
	for i, task := range tasks {
		task.Answer("ok")
		if result := running[i].Wait(); result.Code != 0 {
			t.Fatalf("running call %d = %+v", i, result)
		}
	}
}

func TestAColdModelCallTakesMebibyteInstructionsAndInputOutsideItsArguments(t *testing.T) {
	s, token := modelCallStack(t)
	s.Spare()
	requireStdout(t, s.Attn("settings", "set", "model_calls.agent", "codex"), "codex")
	system := strings.Repeat("An instruction for the call, past the kernel's argument limit.\n", 1<<20/63+1)[:1<<20]
	input := strings.Repeat("A line of a long changelog, past the kernel's argument limit.\n", 1<<20/62+1)[:1<<20]

	for _, harness := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		call := callModel(s, token, map[string]any{"harness": string(harness), "system": system, "input": input})
		task := s.HeadlessTask()
		if task.Harness != harness || slices.Contains(task.Argv, "--input-format") {
			t.Fatalf("a call naming %s reached %+v, want a cold %s", harness, task.Argv, harness)
		}
		if task.Prompt != strings.TrimSpace(system)+"\n\n"+input {
			t.Fatalf("cold %s got a prompt of %d bytes, want the %d-byte instructions and the %d-byte input", harness, len(task.Prompt), len(system), len(input))
		}
		if slices.ContainsFunc(task.Argv, func(arg string) bool {
			return strings.Contains(arg, "A line of a long changelog") || strings.Contains(arg, "An instruction for the call")
		}) {
			t.Fatalf("the instructions or the input are in cold %s's arguments", harness)
		}
		task.Answer("summarized")
		if result := call.Wait(); result.Code != 0 || protocol.Deref(answered(t, result).Text) != "summarized" {
			t.Fatalf("a cold %s call with %d-byte instructions and a %d-byte input = %+v", harness, len(system), len(input), result)
		}
	}
}

func TestTheSpareEndsWhenModelCallsAreOutOfUse(t *testing.T) {
	s, _ := modelCallStack(t)
	spare := s.Spare()
	s.Advance(4 * time.Hour)
	spare.AwaitExited()

	s.TrustedApp().Send(protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: true, IdleSeconds: protocol.Ptr(0.0)})
	spare = s.Spare()
	s.Attn("send", "unpair", "Lens")
	spare.AwaitExited()
}

func TestTheModelLogListsEachCallsFactsAndNothingOfItsContent(t *testing.T) {
	s, lens := modelCallStack(t)
	home := s.TrustedApp().SelectedProfile()
	notes := pairSender(t, s, "Notes")
	side := testworld.Request(s.TrustedApp(), protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: "side", Name: "Side"},
		protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "side" })
	if !side.Success {
		t.Fatalf("create Side: %+v", side)
	}
	other := pairSender(t, s, "Other", side.Profile.ID)
	s.Spare()

	content := map[string]any{"system": "SECRET-INSTRUCTIONS", "input": "SECRET-INPUT"}
	answeredCall := callModel(s, lens, content)
	s.HeadlessTask().Answer("SECRET-ANSWER")
	answered(t, answeredCall.Wait())

	codex := callModel(s, notes, map[string]any{"purpose": "notes-title", "harness": "codex", "tier": "light"})
	codexTask := s.HeadlessTask()
	codexTask.Answer("a title")
	answered(t, codex.Wait())

	limited := callModel(s, lens, map[string]any{"purpose": "slow", "limit_ms": 25000})
	s.AwaitTimer(daemon.ModelCallLimitTimer)
	slow := s.HeadlessTask()
	s.Advance(25 * time.Second)
	limited.Wait()
	slow.AwaitExited()
	slow.Answer("too late")

	stopped := callModel(s, notes, map[string]any{"purpose": "abandoned"})
	abandoned := s.HeadlessTask()
	stopped.Kill()
	abandoned.AwaitExited()
	abandoned.Answer("never read")

	elsewhere := callModel(s, other, map[string]any{"purpose": "side-call"})
	s.HeadlessTask().Answer("ok")
	answered(t, elsewhere.Wait())
	s.AwaitJobsSettled()

	listed := s.Attn("model", "log", "--profile", home, "--json")
	if strings.Contains(listed.Stdout, "SECRET") {
		t.Fatalf("attn model log holds a call's content: %s", listed.Stdout)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(listed.Stdout), &rows); err != nil {
		t.Fatalf("attn model log --json printed %q: %v (stderr %q)", listed.Stdout, err, listed.Stderr)
	}
	facts := []string{"at", "sender", "purpose", "harness", "model", "took_ms", "model_ms", "tokens_in", "tokens_out", "cost_usd", "outcome"}
	for _, row := range rows {
		for field := range row {
			if !slices.Contains(facts, field) {
				t.Errorf("a model log row has %q; a row holds only %v", field, facts)
			}
		}
	}
	type summary struct {
		sender, purpose, harness, model, outcome string
		tookMs, modelMs, tokensIn, tokensOut     float64
	}
	var got []summary
	for _, row := range rows {
		modelMs, _ := row["model_ms"].(float64)
		got = append(got, summary{row["sender"].(string), row["purpose"].(string), row["harness"].(string), row["model"].(string), row["outcome"].(string),
			row["took_ms"].(float64), modelMs, row["tokens_in"].(float64), row["tokens_out"].(float64)})
	}
	want := []summary{
		{"Notes", "abandoned", "claude", "claude-sonnet-fake", "stopped", 0, 0, 0, 0},
		{"Lens", "slow", "claude", "claude-sonnet-fake", "limit", 25000, 0, 0, 0},
		{"Notes", "notes-title", "codex", codexTask.Model, "answered", 0, 0, 0, 0},
		{"Lens", "test", "claude", "claude-sonnet-fake", "answered", 0, fakeagent.FakeModelMs, 661, 12},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("attn model log --json = %+v, want newest first %+v", got, want)
	}
	for _, row := range rows {
		if _, err := time.Parse(time.RFC3339, row["at"].(string)); err != nil {
			t.Errorf("a model log row's at %q is not a time: %v", row["at"], err)
		}
	}

	if onlyNotes := s.Attn("model", "log", "--profile", home, "--sender", "notes", "--json"); strings.Count(onlyNotes.Stdout, `"sender":"Notes"`) != 2 || strings.Contains(onlyNotes.Stdout, "Lens") {
		t.Fatalf("attn model log --sender notes = %s, want Notes's two calls only", onlyNotes.Stdout)
	}
	if sides := s.Attn("model", "log", "--profile", "Side", "--json"); !strings.Contains(sides.Stdout, `"purpose":"side-call"`) || strings.Count(sides.Stdout, `"sender"`) != 1 {
		t.Fatalf("attn model log for Side = %s, want only Other's call", sides.Stdout)
	}

	s.Advance(31 * 24 * time.Hour)
	s.AwaitJobsSettled()
	if kept := s.Attn("model", "log", "--profile", home, "--json"); strings.TrimSpace(kept.Stdout) != "[]" {
		t.Fatalf("31 days on, attn model log = %s, want the calls pruned", kept.Stdout)
	}
}

func containsRun(args []string, run ...string) bool {
	for i := range args {
		if slices.Equal(args[i:min(i+len(run), len(args))], run) {
			return true
		}
	}
	return false
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return lines[len(lines)-1]
}

func TestAModelCallOnASigmaDefaultSaysToPickAHarnessThatAnswers(t *testing.T) {
	s, token := modelCallStack(t)
	spare := s.Spare()
	requireStdout(t, s.Attn("settings", "set", "new_session_agent", "sigma"), "sigma")
	spare.AwaitExited()

	result := s.Run(testworld.Invocation{Args: []string{"model", "call", "--token-stdin"}, Stdin: modelCallStdin(token, nil)})
	want := `model call: model calls default to the new-session agent "sigma", which does not answer model calls; set model_calls.agent to claude or codex`
	if result.Code != 1 || lastLine(result.Stderr) != want {
		t.Fatalf("a call naming nothing with sigma for new sessions = %+v, want exit 1 and %q", result, want)
	}
}

func TestACodexModelCallRunsOnTheSharedAppServerWhileItRuns(t *testing.T) {
	s, token := modelCallStack(t)
	s.AddCodexMCPServer("users-own")
	s.Spare()

	cold := callModel(s, token, map[string]any{"harness": "codex"})
	task := s.HeadlessTask()
	if task.Harness != fakeagent.Codex || !slices.Contains(task.Argv, "exec") {
		t.Fatalf("with no shared app-server running, a Codex call reached %+v, want a cold codex exec", task)
	}
	task.Answer("Cold.")
	answered(t, cold.Wait())

	app := s.App()
	s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	s.CodexServer()
	call := callModel(s, token, map[string]any{"harness": "codex", "tier": "light"})
	task = s.HeadlessTask()
	if task.Harness != fakeagent.Codex || !slices.Contains(task.Argv, "--listen") ||
		task.Model != "gpt-6-luna" || task.Prompt != "Summarize in one line.\n\nthe long text" {
		t.Fatalf("with the shared app-server running, a Codex call reached %+v, want a tool-free conversation on that server with the light model, the instructions and the input", task)
	}
	task.Answer("A one-line summary.")
	if answer := answered(t, call.Wait()); protocol.Deref(answer.Text) != "A one-line summary." || answer.Harness != "codex" || answer.Model != "gpt-6-luna" {
		t.Fatalf("attn model call = %+v", answer)
	}
	if servers := s.CodexServers(); len(servers) != 1 {
		t.Fatalf("Codex app-servers launched = %v, want only the shared one", servers)
	}
}
