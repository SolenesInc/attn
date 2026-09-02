package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func joinArgs(args []string) string {
	return strings.Join(args, "\x00")
}

func assertContainsAll(t *testing.T, label string, args []string, wants ...string) {
	t.Helper()
	joined := joinArgs(args)
	for _, want := range wants {
		if !strings.Contains(joined, want) {
			t.Fatalf("%s missing %q:\n%v", label, want, args)
		}
	}
}

func assertContainsNone(t *testing.T, label string, args []string, forbidden ...string) {
	t.Helper()
	joined := joinArgs(args)
	for _, bad := range forbidden {
		if strings.Contains(joined, bad) {
			t.Fatalf("%s unexpectedly contained %q:\n%v", label, bad, args)
		}
	}
}

func TestClaudeHeadlessArgsUsesFileToolsAndDropsMCPPin(t *testing.T) {
	for _, name := range []string{
		"ANTHROPIC_API_KEY",
		"CLAUDE_CODE_USE_BEDROCK",
		"CLAUDE_CODE_USE_VERTEX",
		"CLAUDE_CODE_USE_FOUNDRY",
	} {
		t.Setenv(name, "")
	}
	args := claudeHeadlessArgs(HeadlessTaskRequest{
		Model:   "claude-test",
		Prompt:  "compact the context file",
		WorkDir: "/tmp/scratch",
	})

	assertContainsAll(t, "Claude native args", args,
		"--print",
		"--setting-sources",
		"--no-session-persistence",
		"--disable-slash-commands",
		"--no-chrome",
		"--allowedTools",
		"Read,Write,Edit,Grep,Glob",
		"--permission-mode",
		"dontAsk",
		"--output-format",
		"json",
		"claude-test",
		"compact the context file",
		// Hermetic MCP: --strict-mcp-config with NO --mcp-config loads zero MCP servers;
		// without it the user's claude.ai account connectors attach and can sink the run.
		"--strict-mcp-config",
	)
	assertContainsNone(t, "Claude native args", args,
		"--mcp-config",
		"--tools",
		"mcp__attn_context__read_context,mcp__attn_context__replace_context",
	)
}

func TestClaudeHeadlessArgsAppendsJudgmentCapsAndSchema(t *testing.T) {
	schema := `{"type":"object","properties":{"assessment":{"type":"string"}},"required":["assessment"]}`
	args := claudeHeadlessArgs(HeadlessTaskRequest{
		Model:        "sonnet",
		Prompt:       "judge the transcript",
		WorkDir:      "/tmp/scratch",
		AllowedTools: []string{"Read", "Grep", "Glob"},
		MaxTurns:     15,
		MaxBudgetUSD: "0.50",
		OutputSchema: []byte(schema),
	})
	assertContainsAll(t, "Claude judgment args", args,
		"--max-turns", "15",
		"--max-budget-usd", "0.50",
		"--json-schema", schema,
		"--allowedTools", "Read,Grep,Glob",
	)

	plain := claudeHeadlessArgs(HeadlessTaskRequest{Model: "sonnet", Prompt: "compact"})
	assertContainsNone(t, "Claude uncapped args", plain,
		"--max-turns", "--max-budget-usd", "--json-schema",
	)
}

func TestClaudeHeadlessArgsHonorsAllowedToolsOverride(t *testing.T) {
	args := claudeHeadlessArgs(HeadlessTaskRequest{
		Model:        "claude-test",
		Prompt:       "compact",
		AllowedTools: []string{"Read", "Write"},
	})
	assertContainsAll(t, "Claude native override args", args, "--allowedTools", "Read,Write")
	assertContainsNone(t, "Claude native override args", args, "Read,Write,Edit,Grep,Glob")
}

func TestClaudeHeadlessArgsReplacesSystemPromptWhenAsked(t *testing.T) {
	args := claudeHeadlessArgs(HeadlessTaskRequest{
		Model:        "claude-test",
		Prompt:       "classify this",
		SystemPrompt: "You are a terse classifier.",
	})
	assertContainsAll(t, "Claude system-prompt args", args,
		"--system-prompt", "You are a terse classifier.")
	// --system-prompt REPLACES; --append-system-prompt (the interactive-launch flag)
	// adds. Emitting the wrong one loses the whole saving silently.
	assertContainsNone(t, "Claude system-prompt args", args, "--append-system-prompt")
	if args[len(args)-1] != "classify this" {
		t.Fatalf("prompt must stay last, got %q:\n%v", args[len(args)-1], args)
	}

	plain := claudeHeadlessArgs(HeadlessTaskRequest{Model: "claude-test", Prompt: "compact"})
	assertContainsNone(t, "Claude default system prompt", plain, "--system-prompt")

	// A tool-less run drops the tool definitions from the billed prefix, unless it
	// asks for a schema: --disallowedTools also disables StructuredOutput.
	toolless := claudeHeadlessArgs(HeadlessTaskRequest{
		Model: "claude-test", Prompt: "classify", DisableTools: true,
	})
	assertContainsAll(t, "Claude tool-less args", toolless, "--disallowedTools", "*")
	assertContainsNone(t, "Claude tool-less args", toolless, "--allowedTools")
	schema := claudeHeadlessArgs(HeadlessTaskRequest{
		Model: "claude-test", Prompt: "classify", DisableTools: true,
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	})
	assertContainsAll(t, "Claude tool-less schema args", schema, "--allowedTools", "")
	assertContainsNone(t, "Claude tool-less schema args", schema, "--disallowedTools")
}

func TestCodexHeadlessArgsUsesWorkspaceWriteAndDropsMCPPin(t *testing.T) {
	args := codexHeadlessArgs(HeadlessTaskRequest{
		Model:   "gpt-test",
		Prompt:  "compact the context file",
		WorkDir: "/tmp/scratch",
	}, 0)

	assertContainsAll(t, "Codex native args", args,
		"exec",
		"--json",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--strict-config",
		"--skip-git-repo-check",
		"--sandbox",
		"workspace-write",
		`approval_policy="never"`,
		"features.apps=false",
		"features.browser_use=false",
		"features.standalone_web_search=false",
		"gpt-test",
		"compact the context file",
	)
	assertContainsNone(t, "Codex native args", args,
		"read-only",
		"features.shell_tool=false",
		"features.unified_exec=false",
		"mcp_servers.attn_context.command",
		"mcp_servers.attn_context.required=true",
		`mcp_servers.attn_context.enabled_tools=["read_context","replace_context"]`,
		`mcp_servers.attn_context.default_tools_approval_mode="approve"`,
	)
}

func TestCodexToolFreeHeadlessArgsExposeOnlyThePrompt(t *testing.T) {
	args := codexToolFreeHeadlessArgs(HeadlessTaskRequest{
		Model:           "gpt-test",
		ReasoningEffort: "low",
		Prompt:          "judge these supplied turns",
		WorkDir:         "/tmp/scratch",
		ExtraWritableRoots: []string{
			"/must-not-be-writable",
		},
	}, 12345)

	assertContainsAll(t, "Codex tool-free args", args,
		"exec",
		"--json",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--strict-config",
		"--skip-git-repo-check",
		"--sandbox",
		"read-only",
		`approval_policy="never"`,
		`model_reasoning_effort="low"`,
		"features.shell_tool=false",
		"features.unified_exec=false",
		`web_search="disabled"`,
		"features.apps=false",
		"features.plugins=false",
		"features.browser_use=false",
		"features.standalone_web_search=false",
		"model_auto_compact_token_limit=12345",
		"gpt-test",
		"judge these supplied turns",
	)
	assertContainsNone(t, "Codex tool-free args", args,
		"workspace-write",
		"--add-dir",
		"/must-not-be-writable",
		"mcp_servers.",
	)
}

func TestCopilotToolFreeHeadlessArgsExposeOnlyThePrompt(t *testing.T) {
	args := copilotToolFreeHeadlessArgs(HeadlessTaskRequest{
		Model:           "claude-sonnet-4.6",
		ReasoningEffort: "high",
		Prompt:          "judge the supplied seed",
		SystemPrompt:    "Return only JSON.",
		DisableTools:    true,
	})

	assertContainsAll(t, "Copilot tool-free args", args,
		"-p",
		"Return only JSON.\n\njudge the supplied seed",
		"-s",
		"--model",
		"claude-sonnet-4.6",
		"--effort",
		"high",
		"--available-tools=ask_user",
		"--disable-builtin-mcps",
		"--no-ask-user",
		"--no-auto-update",
		"--no-bash-env",
		"--no-custom-instructions",
		"--no-experimental",
		"--no-remote",
		"--no-remote-export",
	)
	assertContainsNone(t, "Copilot tool-free args", args,
		"--allow-all-tools",
		"--allow-tool",
		"--reasoning-effort",
	)
	for _, arg := range args {
		if arg == "--available-tools=" {
			t.Fatal("Copilot tool-free args used an empty allowlist, which the CLI treats as no filter")
		}
	}
}
