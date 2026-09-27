package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func envEntry(env []string, key string) (string, bool) {
	for _, pair := range env {
		if value, ok := strings.CutPrefix(pair, key+"="); ok {
			return value, true
		}
	}
	return "", false
}

func hasArgs(argv []string, want ...string) bool {
	for i := range argv {
		if len(argv)-i >= len(want) && slices.Equal(argv[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

func firstPathEntry(env []string) string {
	path, _ := envEntry(env, "PATH")
	return filepath.SplitList(path)[0]
}

func titleTaskFor(t *testing.T, w *world, h fakeagent.Harness) *fakeagent.HeadlessTask {
	t.Helper()
	app := w.App()
	session := w.Spawn(app, h, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("investigate the retry queue")
	})
	agent := w.Launched(session)
	task := w.HeadlessTask()
	if task.Harness != h {
		t.Fatalf("the title task went to %s, want %s", task.Harness, h)
	}
	task.Answer("Retry queue investigation")
	awaitLabel(app, session, "Retry queue investigation")
	agent.Prompted()
	return task
}

func TestToolFreeHeadlessTasksRunReadOnlyWithOnlyTheirProvidersEnvironment(t *testing.T) {
	for _, tc := range []struct {
		harness      fakeagent.Harness
		readOnly     [][]string
		forbidden    []string
		keptEnv      []string
		droppedEnv   []string
		fixedEnv     []string
		capped       func(task *fakeagent.HeadlessTask) bool
		homeIsTheRun bool
	}{
		{
			harness: fakeagent.Claude,
			readOnly: [][]string{
				{"--disallowedTools", "*"}, {"--permission-mode", "dontAsk"}, {"--setting-sources", ""},
				{"--strict-mcp-config"}, {"--no-session-persistence"}, {"--disable-slash-commands"},
			},
			forbidden:  []string{"--mcp-config", "--allowedTools", "--bare", "--add-dir"},
			keptEnv:    []string{"ANTHROPIC_BASE_URL"},
			droppedEnv: []string{"OPENAI_BASE_URL", "GH_TOKEN", "UNRELATED_SECRET", "ATTN_SESSION_ID", "CODEX_THREAD_ID"},
			fixedEnv:   []string{"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"},
			capped: func(task *fakeagent.HeadlessTask) bool {
				window, _ := envEntry(task.Env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW")
				return window == "150000"
			},
		},
		{
			harness: fakeagent.Codex,
			readOnly: [][]string{
				{"--sandbox", "read-only"}, {"-c", `approval_policy="never"`}, {"-c", "features.shell_tool=false"},
				{"-c", `web_search="disabled"`}, {"--ignore-user-config"}, {"--ignore-rules"}, {"--ephemeral"},
			},
			forbidden:  []string{"workspace-write", "--add-dir", "--dangerously-bypass-approvals-and-sandbox"},
			keptEnv:    []string{"OPENAI_BASE_URL", "CODEX_HOME"},
			droppedEnv: []string{"ANTHROPIC_BASE_URL", "GH_TOKEN", "UNRELATED_SECRET", "ATTN_SESSION_ID", "CODEX_THREAD_ID"},
			capped: func(task *fakeagent.HeadlessTask) bool {
				return slices.Contains(task.Argv, "model_auto_compact_token_limit=150000")
			},
		},
		{
			harness: fakeagent.Copilot,
			readOnly: [][]string{
				{"--available-tools=ask_user"}, {"--disable-builtin-mcps"}, {"--no-custom-instructions"}, {"--no-ask-user"},
			},
			forbidden:    []string{"--allow-all-tools", "--allow-tool", "--yolo"},
			keptEnv:      []string{"GH_TOKEN"},
			droppedEnv:   []string{"ANTHROPIC_BASE_URL", "OPENAI_BASE_URL", "COPILOT_ALLOW_ALL", "UNRELATED_SECRET", "ATTN_SESSION_ID"},
			fixedEnv:     []string{"COPILOT_MCP_TOOL_CACHE=false"},
			homeIsTheRun: true,
		},
	} {
		t.Run(string(tc.harness), func(t *testing.T) {
			w := newTitlingWorld(t, tc.harness)
			for _, name := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
				t.Setenv(name, "")
			}
			for key, value := range map[string]string{
				"ANTHROPIC_BASE_URL": "https://anthropic.example", "OPENAI_BASE_URL": "https://openai.example",
				"GH_TOKEN": "gh-token", "UNRELATED_SECRET": "secret", "ATTN_SESSION_ID": "someone-else",
				"CODEX_THREAD_ID": "thread", "COPILOT_ALLOW_ALL": "1",
			} {
				t.Setenv(key, value)
			}
			setSetting(t, w.App(), "headless_context_window_cap", "150000")

			task := titleTaskFor(t, w, tc.harness)
			for _, flag := range tc.readOnly {
				if !hasArgs(task.Argv, flag...) {
					t.Errorf("the title task ran %s without %q: %q", tc.harness, flag, task.Argv)
				}
			}
			for _, flag := range tc.forbidden {
				if slices.Contains(task.Argv, flag) {
					t.Errorf("the title task ran %s with %s: %q", tc.harness, flag, task.Argv)
				}
			}
			for _, arg := range task.Argv {
				if strings.HasPrefix(arg, "mcp_servers.") {
					t.Errorf("the title task gave %s a tool server: %q", tc.harness, arg)
				}
			}
			for _, key := range tc.keptEnv {
				if _, ok := envEntry(task.Env, key); !ok {
					t.Errorf("the title task ran %s without its provider's %s", tc.harness, key)
				}
			}
			for _, key := range tc.droppedEnv {
				if value, ok := envEntry(task.Env, key); ok {
					t.Errorf("the title task handed %s %s=%s", tc.harness, key, value)
				}
			}
			for _, pair := range tc.fixedEnv {
				if !slices.Contains(task.Env, pair) {
					t.Errorf("the title task ran %s without %s", tc.harness, pair)
				}
			}
			if tc.capped != nil && !tc.capped(task) {
				t.Errorf("the title task ran %s without the headless context window cap: %q", tc.harness, task.Argv)
			}
			if home, _ := envEntry(task.Env, "COPILOT_HOME"); tc.homeIsTheRun && (home == "" || home != task.Dir) {
				t.Errorf("the title task ran copilot with home %q from %q, want its own scratch directory", home, task.Dir)
			}
		})
	}
}

func TestClaudeWithAnAPIKeyRunsHeadlessTasksBare(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	task := titleTaskFor(t, w, fakeagent.Claude)
	if !slices.Contains(task.Argv, "--bare") || slices.Contains(task.Argv, "--setting-sources") {
		t.Errorf("with an API key claude ran the title task as %q, want --bare instead of filtered settings", task.Argv)
	}
	if key, _ := envEntry(task.Env, "ANTHROPIC_API_KEY"); key != "sk-test" {
		t.Errorf("the bare title task ran without the API key")
	}
}

func TestEachHarnessJudgesATurnWithABoundedToolFreeVerdictRun(t *testing.T) {
	t.Run("claude", func(t *testing.T) {
		w := newTitlingWorld(t, fakeagent.Claude)
		t.Setenv("ATTN_CLAUDE_CLASSIFIER_MODEL", "judge-haiku")
		app := w.App()
		session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
		agent := w.Launched(session)
		app.TypeLine(session, "deploy to staging")
		agent.Prompted()
		w.HeadlessTask().Answer("Staging deploy")

		agent.Reply("Staging is ready. Should I deploy to production too?")
		verdict := w.HeadlessTask()
		schema, _ := flagValue(verdict.Argv, "--json-schema")
		var shape struct {
			Required []string `json:"required"`
		}
		if err := json.Unmarshal([]byte(schema), &shape); err != nil || !slices.Contains(shape.Required, "verdict") {
			t.Errorf("the verdict run's schema %q does not require a verdict (%v)", schema, err)
		}
		if tools, ok := flagValue(verdict.Argv, "--allowedTools"); !ok || tools != "" {
			t.Errorf("the verdict run allowed tools %q (present %t), want none", tools, ok)
		}
		if turns, _ := flagValue(verdict.Argv, "--max-turns"); turns != "2" || verdict.Model != "judge-haiku" {
			t.Errorf("the verdict run took %s turns on %q, want 2 on the configured classifier model", turns, verdict.Model)
		}
		if !strings.Contains(verdict.Prompt, "Should I deploy to production too?") {
			t.Fatalf("the verdict run judged %q, want the turn's last message", verdict.Prompt)
		}
		verdict.Answer(`{"verdict":"WAITING"}`)
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	})

	t.Run("codex", func(t *testing.T) {
		w := newTitlingWorld(t, fakeagent.Codex)
		t.Setenv("ATTN_CODEX_CLASSIFIER_MODEL", "judge-mini")
		t.Setenv("ATTN_CODEX_CLASSIFIER_REASONING_EFFORT", "minimal")
		app := w.App()
		cwd := w.Path("shop")
		session := w.Spawn(app, fakeagent.Codex, cwd)
		agent := w.Launched(session)
		app.TypeLine(session, "deploy to staging")
		agent.Prompted()
		w.HeadlessTask().Answer("Staging deploy")

		agent.Reply("Staging is deployed.")
		verdict := w.HeadlessTask()
		if verdict.Model != "judge-mini" || verdict.Effort != "minimal" {
			t.Errorf("the verdict run used %q at %q, want the configured classifier model and effort", verdict.Model, verdict.Effort)
		}
		for _, flag := range []string{"--ignore-user-config", "--ephemeral", "features.shell_tool=false"} {
			if !slices.Contains(verdict.Argv, flag) {
				t.Errorf("the verdict run lacks %s: %q", flag, verdict.Argv)
			}
		}
		if dir, _ := filepath.EvalSymlinks(verdict.Dir); dir != cwd {
			t.Errorf("the verdict run ran in %q, want the session's directory %q", verdict.Dir, cwd)
		}
		verdict.Answer(`{"verdict":"DONE"}`)
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	})
}

func TestAgentsFindTheActiveAttnFirstOnTheirPath(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	stale := filepath.Join(t.TempDir(), "stale-attn")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "attn"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stale+string(os.PathListSeparator)+os.Getenv("PATH"))
	active := filepath.Dir(os.Getenv("ATTN_WRAPPER_PATH"))

	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("investigate the retry queue")
	})
	agent := w.Launched(session)
	title := w.HeadlessTask()
	title.Answer("Retry queue investigation")
	agent.Prompted()
	for name, env := range map[string][]string{"the agent": agent.Env, "the title task": title.Env} {
		if first := firstPathEntry(env); first != active {
			t.Errorf("%s runs with PATH starting at %q, want the active attn's %q", name, first, active)
		}
	}
}
