package agent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func envHasCap(env []string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=") {
			return true
		}
	}
	return false
}

func TestClaudeBuildEnv_ContextWindowCap(t *testing.T) {
	t.Run("chief launch emits the cap", func(t *testing.T) {
		env := (&Claude{}).BuildEnv(SpawnOpts{NotebookRoot: "/nb", AutoCompactWindow: 200000})
		if !slices.Contains(env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=200000") {
			t.Fatalf("chief env missing the cap: %#v", env)
		}
	})

	t.Run("delegated launch emits the cap too", func(t *testing.T) {
		env := (&Claude{}).BuildEnv(SpawnOpts{WorkspaceContextPath: "/ws", AutoCompactWindow: 800000})
		if !slices.Contains(env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=800000") {
			t.Fatalf("delegated env missing the cap: %#v", env)
		}
	})

	t.Run("no cap emits nothing", func(t *testing.T) {
		for _, opts := range []SpawnOpts{
			{NotebookRoot: "/nb", AutoCompactWindow: 0},
			{WorkspaceContextPath: "/ws", AutoCompactWindow: 0},
		} {
			if env := (&Claude{}).BuildEnv(opts); envHasCap(env) {
				t.Fatalf("uncapped env unexpectedly carried the cap: %#v", env)
			}
		}
	})
}

// Claude Code copies each settings file's `env` block over its own environment, and
// the generated --settings is applied after the user's, so the cap must appear there.
func TestClaudeGenerateHooksConfig_ContextWindowCap(t *testing.T) {
	parseEnv := func(t *testing.T, content string) map[string]string {
		t.Helper()
		var parsed struct {
			Env map[string]string `json:"env"`
		}
		if err := json.Unmarshal([]byte(content), &parsed); err != nil {
			t.Fatalf("settings config is not valid JSON: %v", err)
		}
		return parsed.Env
	}

	t.Run("capped launch pins the window in the settings file", func(t *testing.T) {
		content := (&Claude{}).GenerateHooksConfig(SpawnOpts{
			SessionID:         "sess-1",
			SocketPath:        "/tmp/attn.sock",
			WrapperPath:       "/tmp/attn",
			AutoCompactWindow: 200000,
		})
		if got := parseEnv(t, content)["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "200000" {
			t.Fatalf("settings env cap = %q, want %q; content: %s", got, "200000", content)
		}
	})

	t.Run("uncapped launch writes no env block", func(t *testing.T) {
		content := (&Claude{}).GenerateHooksConfig(SpawnOpts{
			SessionID:   "sess-1",
			SocketPath:  "/tmp/attn.sock",
			WrapperPath: "/tmp/attn",
		})
		if env := parseEnv(t, content); len(env) != 0 {
			t.Fatalf("uncapped settings unexpectedly carried env %#v", env)
		}
		if strings.Contains(content, "CLAUDE_CODE_AUTO_COMPACT_WINDOW") {
			t.Fatalf("uncapped settings mentioned the cap: %s", content)
		}
	})
}

func TestCodexBuildCommand_ContextWindowCap(t *testing.T) {
	t.Run("chief launch emits the cap override", func(t *testing.T) {
		cmd := (&Codex{}).BuildCommand(SpawnOpts{
			SessionID:         "sess-1",
			CWD:               "/tmp/project",
			Executable:        "codex",
			NotebookRoot:      "/nb",
			AutoCompactWindow: 200000,
		})
		if !argvHasPair(cmd.Args, "-c", "model_auto_compact_token_limit=200000") {
			t.Fatalf("chief codex args missing the cap override: %#v", cmd.Args)
		}
	})

	t.Run("delegated launch emits the cap override too", func(t *testing.T) {
		cmd := (&Codex{}).BuildCommand(SpawnOpts{
			SessionID:            "sess-1",
			CWD:                  "/tmp/project",
			Executable:           "codex",
			WorkspaceContextPath: "/ws",
			AutoCompactWindow:    800000,
		})
		if !argvHasPair(cmd.Args, "-c", "model_auto_compact_token_limit=800000") {
			t.Fatalf("delegated codex args missing the cap override: %#v", cmd.Args)
		}
	})

	t.Run("no cap emits nothing", func(t *testing.T) {
		cmd := (&Codex{}).BuildCommand(SpawnOpts{
			SessionID:  "sess-1",
			CWD:        "/tmp/project",
			Executable: "codex",
		})
		for _, arg := range cmd.Args {
			if strings.HasPrefix(arg, "model_auto_compact_token_limit=") {
				t.Fatalf("uncapped codex args unexpectedly carried a cap override: %#v", cmd.Args)
			}
		}
	})
}

func TestSetHeadlessContextWindowCap_ClampsNegative(t *testing.T) {
	t.Cleanup(func() { SetHeadlessContextWindowCap(0) })
	SetHeadlessContextWindowCap(-5)
	if got := HeadlessContextWindowCap(); got != 0 {
		t.Fatalf("negative cap not clamped: got %d, want 0", got)
	}
	SetHeadlessContextWindowCap(150000)
	if got := HeadlessContextWindowCap(); got != 150000 {
		t.Fatalf("cap not stored: got %d, want 150000", got)
	}
}

func TestHeadlessEnvironment_ClaudeContextWindowCap(t *testing.T) {
	t.Cleanup(func() { SetHeadlessContextWindowCap(0) })

	SetHeadlessContextWindowCap(150000)
	claudeEnv := headlessEnvironment("claude")
	if !slices.Contains(claudeEnv, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=150000") {
		t.Fatalf("claude headless env missing the cap: %#v", claudeEnv)
	}
	if envHasCap(headlessEnvironment("codex")) {
		t.Fatalf("codex headless env unexpectedly carried the Claude cap")
	}

	SetHeadlessContextWindowCap(0)
	if envHasCap(headlessEnvironment("claude")) {
		t.Fatalf("uncapped headless env unexpectedly carried the cap")
	}
}

func TestCodexHeadlessArgs_ContextWindowCap(t *testing.T) {
	t.Run("native path emits the override when capped", func(t *testing.T) {
		args := codexHeadlessArgs(HeadlessTaskRequest{Model: "gpt-test", Prompt: "narrate"}, 150000)
		if !argvHasPair(args, "-c", "model_auto_compact_token_limit=150000") {
			t.Fatalf("native codex args missing the cap override: %#v", args)
		}
	})

	t.Run("MCP path emits the override when capped", func(t *testing.T) {
		args := buildCodexHeadlessArgs(HeadlessTaskRequest{Model: "gpt-test", Prompt: "judge"}, "", 150000)
		if !argvHasPair(args, "-c", "model_auto_compact_token_limit=150000") {
			t.Fatalf("MCP codex args missing the cap override: %#v", args)
		}
	})

	t.Run("uncapped emits nothing", func(t *testing.T) {
		for _, args := range [][]string{
			codexHeadlessArgs(HeadlessTaskRequest{Model: "gpt-test", Prompt: "p"}, 0),
			buildCodexHeadlessArgs(HeadlessTaskRequest{Model: "gpt-test", Prompt: "p"}, "", 0),
		} {
			for _, arg := range args {
				if arg == "model_auto_compact_token_limit=0" || arg == "model_auto_compact_token_limit=" {
					t.Fatalf("uncapped codex args unexpectedly carried a cap override: %#v", args)
				}
			}
		}
	})
}
