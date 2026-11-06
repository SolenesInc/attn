package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/victorarias/attn/internal/fakeclaudeapi"
)

const (
	hookCommand     = "record-hook"
	idleCheckAfter  = 54 * time.Minute
	idleCheckSlack  = 5 * time.Second
	skippedAfter    = idleCheckAfter + 2*time.Minute
	largeContext    = 245000
	smallContext    = 168000
	bootDeadline    = 60 * time.Second
	turnDeadline    = 60 * time.Second
	compactDeadline = 60 * time.Second
)

type scenario struct {
	name     string
	context  int
	cache1h  bool
	broken   bool
	compacts bool
	hooks    []string
}

var scenarios = []scenario{
	{name: "compacts", context: largeContext, cache1h: true, compacts: true, hooks: []string{"PreCompact:auto", "SessionStart:compact", "PostCompact:auto"}},
	{name: "under the threshold", context: smallContext, cache1h: true},
	{name: "5-minute cache", context: largeContext},
	{name: "failed compact", context: largeContext, cache1h: true, broken: true, compacts: true, hooks: []string{"PreCompact:auto"}},
}

type hookEntry struct {
	Event   string          `json:"event"`
	At      time.Time       `json:"at"`
	Payload json.RawMessage `json:"payload"`
}

func recordHook(args []string) {
	if len(args) != 2 {
		fail("usage: fakeclaudeapi record-hook <event> <file>")
	}
	payload, err := io.ReadAll(os.Stdin)
	if err != nil || !json.Valid(payload) {
		payload = []byte("null")
	}
	line, _ := json.Marshal(hookEntry{Event: args[0], At: time.Now(), Payload: payload})
	file, err := os.OpenFile(args[1], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fail(err.Error())
	}
	defer file.Close()
	_, _ = file.Write(append(line, '\n'))
}

func readHooks(path string) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	var hooks []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<24)
	for scanner.Scan() {
		var entry hookEntry
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		var payload struct {
			Trigger string `json:"trigger"`
			Source  string `json:"source"`
		}
		_ = json.Unmarshal(entry.Payload, &payload)
		name := entry.Event
		if detail := payload.Trigger + payload.Source; detail != "" {
			name += ":" + detail
		}
		hooks = append(hooks, name)
	}
	return hooks
}

type run struct {
	scenario
	model *fakeclaudeapi.Model
	hooks string
	input *os.File
}

func (r *run) count(event string) int {
	n := 0
	for _, hook := range readHooks(r.hooks) {
		if strings.HasPrefix(hook, event) {
			n++
		}
	}
	return n
}

func (r *run) await(event string, want int, deadline time.Duration) error {
	limit := time.Now().Add(deadline)
	for r.count(event) < want {
		if time.Now().After(limit) {
			return fmt.Errorf("no %s within %s; hooks: %v", event, deadline, readHooks(r.hooks))
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

func (r *run) prompt(text string) error {
	submitted := r.count("UserPromptSubmit")
	if _, err := r.input.WriteString(text); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	limit := time.Now().Add(turnDeadline)
	for r.count("UserPromptSubmit") == submitted {
		if time.Now().After(limit) {
			return fmt.Errorf("the prompt was not submitted within %s", turnDeadline)
		}
		if _, err := r.input.WriteString("\r"); err != nil {
			return err
		}
		time.Sleep(3 * time.Second)
	}
	return r.await("Stop", r.count("UserPromptSubmit"), turnDeadline)
}

func (r *run) check() error {
	if err := r.await("SessionStart:startup", 1, bootDeadline); err != nil {
		return err
	}
	time.Sleep(3 * time.Second)
	if err := r.prompt("say hello"); err != nil {
		return err
	}
	lastRequest := r.model.LastRequestAt()
	before := len(readHooks(r.hooks))
	due := lastRequest.Add(idleCheckAfter)
	if r.compacts {
		if err := r.await("PreCompact", 1, time.Until(due)+compactDeadline); err != nil {
			return err
		}
		fired := time.Now()
		if fired.Before(due.Add(-idleCheckSlack)) || fired.After(due.Add(idleCheckSlack)) {
			return fmt.Errorf("the compact started %s after the last request; want %s", fired.Sub(lastRequest).Round(time.Second), idleCheckAfter)
		}
		time.Sleep(compactDeadline)
	} else {
		time.Sleep(time.Until(lastRequest.Add(skippedAfter)))
	}
	got := []string{}
	for _, hook := range readHooks(r.hooks)[before:] {
		if !strings.HasPrefix(hook, "Notification") {
			got = append(got, hook)
		}
	}
	if !slices.Equal(got, r.scenario.hooks) && !(len(got) == 0 && len(r.scenario.hooks) == 0) {
		return fmt.Errorf("hooks after the turn were %v; want %v", got, r.scenario.hooks)
	}
	wantCompacts := 0
	if r.compacts {
		wantCompacts = 1
	}
	if count := r.model.CompactCount(); count != wantCompacts {
		return fmt.Errorf("the model saw %d compact requests; want %d", count, wantCompacts)
	}
	mark := r.model.Mark()
	if err := r.prompt("say hello again"); err != nil {
		return err
	}
	if !slices.ContainsFunc(r.model.Prompts()[mark:], func(prompt string) bool { return strings.Contains(prompt, "say hello again") }) {
		return fmt.Errorf("the model did not see the prompt after the idle wait")
	}
	return nil
}

func start(s scenario, root, claude, self string) (*run, func(), error) {
	dir := filepath.Join(root, strings.ReplaceAll(s.name, " ", "-"))
	work, config := filepath.Join(dir, "work"), filepath.Join(dir, "config")
	if err := fakeclaudeapi.WriteClaudeConfig(claude, config, work); err != nil {
		return nil, nil, err
	}
	model := fakeclaudeapi.New()
	model.Server = httptest.NewServer(model)
	model.SetContextTokens(s.context)
	model.BreakCompacts(s.broken)
	hooks := filepath.Join(dir, "hooks.jsonl")
	settings := map[string]any{}
	hookSettings := map[string]any{}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "Stop", "StopFailure", "Notification", "PreCompact", "PostCompact"} {
		hookSettings[event] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": fmt.Sprintf("%q %s %s %q", self, hookCommand, event, hooks)}}}}
	}
	settings["hooks"] = hookSettings
	encoded, err := json.Marshal(settings)
	if err != nil {
		return nil, nil, err
	}
	command := exec.Command(claude, "--setting-sources", "project,local", "--settings", string(encoded), "--model", "opus", "--effort", "low")
	command.Dir = work
	command.Env = []string{
		"HOME=" + os.Getenv("HOME"), "USER=" + os.Getenv("USER"), "LOGNAME=" + os.Getenv("USER"),
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "SHELL=/bin/sh", "TERM=xterm-256color", "LANG=en_US.UTF-8",
		"CLAUDE_CONFIG_DIR=" + config, "ANTHROPIC_BASE_URL=" + model.URL, "ANTHROPIC_API_KEY=" + fakeclaudeapi.APIKey,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1",
	}
	if s.cache1h {
		command.Env = append(command.Env, "ENABLE_PROMPT_CACHING_1H=1")
	}
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		model.Close()
		return nil, nil, err
	}
	go func() { _, _ = io.Copy(io.Discard, terminal) }()
	stop := func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
		_ = terminal.Close()
		model.Close()
	}
	return &run{scenario: s, model: model, hooks: hooks, input: terminal}, stop, nil
}

func checkIdle() {
	claude, err := exec.LookPath("claude")
	if err != nil {
		fail("claude is not on PATH")
	}
	version, err := exec.Command(claude, "--version").Output()
	if err != nil {
		fail(err.Error())
	}
	self, err := os.Executable()
	if err != nil {
		fail(err.Error())
	}
	root, err := os.MkdirTemp("", "claude-idle-check-")
	if err != nil {
		fail(err.Error())
	}
	defer os.RemoveAll(root)
	fmt.Printf("claude %s: checking idle compaction; this takes about %s\n", strings.TrimSpace(string(version)), skippedAfter+2*time.Minute)
	results := make([]error, len(scenarios))
	var wg sync.WaitGroup
	for i, s := range scenarios {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, stop, err := start(s, root, claude, self)
			if err != nil {
				results[i] = err
				return
			}
			defer stop()
			results[i] = r.check()
		}()
	}
	wg.Wait()
	failed := false
	for i, s := range scenarios {
		if results[i] != nil {
			failed = true
			fmt.Printf("FAIL %s: %v\n", s.name, results[i])
			continue
		}
		fmt.Printf("ok   %s\n", s.name)
	}
	if failed {
		fmt.Println("Real Claude no longer matches docs/internal/harnesses/claude.md. Update the doc, the fake API and attn's handling together.")
		os.RemoveAll(root)
		os.Exit(1)
	}
}
