package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/toolhome"
)

func writeHeadlessArgsRecorder(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "args.log")
	scriptPath := filepath.Join(dir, "agent")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellSingleQuote(logPath) + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	return scriptPath, logPath
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func TestCodexRunHeadlessTaskScopesToolsAndConfiguration(t *testing.T) {
	executable, logPath := writeHeadlessArgsRecorder(t)
	_, err := (&Codex{}).RunHeadlessTask(context.Background(), HeadlessTaskRequest{
		Executable: executable,
		Model:      "gpt-test",
		Prompt:     "compact",
		WorkDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("RunHeadlessTask error: %v", err)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := string(args)
	for _, want := range []string{
		"exec",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--strict-config",
		"--skip-git-repo-check",
		"workspace-write",
		`approval_policy="never"`,
		"features.apps=false",
		"features.hooks=false",
		"features.plugins=false",
		"features.browser_use=false",
		"features.memories=false",
		"features.multi_agent=false",
		"features.standalone_web_search=false",
		"gpt-test",
		"compact",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Codex args missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{
		"read-only",
		"features.shell_tool=false",
		"features.unified_exec=false",
		"mcp_servers.attn_context",
		"read_context",
		"replace_context",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("Codex args unexpectedly contained %q:\n%s", forbidden, got)
		}
	}
}

func TestHeadlessEnvironment_CodexSetsDefaultHomeWhenUnset(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	homeDir := t.TempDir()
	t.Setenv(toolhome.EnvVar, homeDir)
	if !environmentContains(headlessEnvironment("codex", ""), "CODEX_HOME") {
		t.Fatal("Codex headless environment did not set CODEX_HOME")
	}
	want := "CODEX_HOME=" + filepath.Join(homeDir, ".codex")
	if !strings.Contains(strings.Join(headlessEnvironment("codex", ""), "\n"), want) {
		t.Fatalf("Codex headless environment missing %q", want)
	}
}

func TestHeadlessEnvironment_CopilotKeepsAuthenticationAndDropsBehaviorOverrides(t *testing.T) {
	t.Setenv("COPILOT_GITHUB_TOKEN", "test-token")
	t.Setenv("COPILOT_HOME", "/unsafe/persisted-home")
	t.Setenv("COPILOT_ALLOW_ALL", "1")
	t.Setenv("COPILOT_MODEL", "wrong-model")
	t.Setenv("GITHUB_COPILOT_PROMPT_MODE_EXTENSIONS", "true")
	t.Setenv("GITHUB_COPILOT_PROMPT_MODE_REPO_HOOKS", "true")
	t.Setenv("GITHUB_COPILOT_PROMPT_MODE_WORKSPACE_MCP", "true")

	scratch := t.TempDir()
	env := headlessEnvironment("copilot", scratch)
	for _, name := range []string{"COPILOT_GITHUB_TOKEN", "COPILOT_HOME", "COPILOT_MCP_TOOL_CACHE"} {
		if !environmentContains(env, name) {
			t.Fatalf("Copilot headless environment did not include %s", name)
		}
	}
	for _, name := range []string{
		"COPILOT_ALLOW_ALL",
		"COPILOT_MODEL",
		"GITHUB_COPILOT_PROMPT_MODE_EXTENSIONS",
		"GITHUB_COPILOT_PROMPT_MODE_REPO_HOOKS",
		"GITHUB_COPILOT_PROMPT_MODE_WORKSPACE_MCP",
	} {
		if environmentContains(env, name) {
			t.Fatalf("Copilot headless environment leaked %s", name)
		}
	}
	if !slices.Contains(env, "COPILOT_HOME="+scratch) {
		t.Fatalf("Copilot headless environment did not isolate its home: %#v", env)
	}
	if slices.Contains(env, "COPILOT_HOME=/unsafe/persisted-home") {
		t.Fatalf("Copilot headless environment reused persisted state: %#v", env)
	}
}

func TestCopilotRunHeadlessTaskUsesToolFreeProgrammaticMode(t *testing.T) {
	executable, logPath := writeHeadlessArgsRecorder(t)
	result, err := (&Copilot{}).RunHeadlessTask(context.Background(), HeadlessTaskRequest{
		Executable:      executable,
		Model:           "claude-sonnet-4.6",
		ReasoningEffort: "high",
		Prompt:          "classify",
		WorkDir:         t.TempDir(),
		DisableTools:    true,
	})
	if err != nil {
		t.Fatalf("RunHeadlessTask error: %v", err)
	}
	if result.Text != "" {
		t.Fatalf("result text = %q, want empty recorder output", result.Text)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := string(args)
	for _, want := range []string{
		"-p",
		"classify",
		"-s",
		"--model",
		"claude-sonnet-4.6",
		"--effort",
		"high",
		"--available-tools=ask_user",
		"--disable-builtin-mcps",
		"--no-custom-instructions",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Copilot args missing %q:\n%s", want, got)
		}
	}
}

func TestCopilotRunHeadlessTaskRejectsCapabilitiesItCannotIsolate(t *testing.T) {
	provider := &Copilot{}
	for _, request := range []HeadlessTaskRequest{
		{DisableTools: false},
		{DisableTools: true, AllowedTools: []string{"view"}},
		{DisableTools: true, MCPServerName: "external"},
	} {
		if _, err := provider.RunHeadlessTask(context.Background(), request); err == nil {
			t.Fatalf("RunHeadlessTask(%+v) succeeded", request)
		}
	}
}

func TestCodexRunHeadlessTaskScopesSingleToolNameAndCapturesLastMessage(t *testing.T) {
	executable, logPath := writeHeadlessArgsRecorder(t)
	_, err := (&Codex{}).RunHeadlessTask(context.Background(), HeadlessTaskRequest{
		Executable:       executable,
		Model:            "gpt-test",
		Prompt:           "answer",
		WorkDir:          t.TempDir(),
		MCPServerName:    "attn_workflow_result",
		MCPServerCommand: "/tmp/attn",
		ToolName:         "return_result",
		MCPServerArgs:    []string{"_workflow-result-mcp", "--result-file", "/tmp/result"},
	})
	if err != nil {
		t.Fatalf("RunHeadlessTask error: %v", err)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := string(args)
	for _, want := range []string{
		"--output-last-message",
		`mcp_servers.attn_workflow_result.enabled_tools=["return_result"]`,
		"mcp_servers.attn_workflow_result.required=true",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Codex args missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "read_context") || strings.Contains(got, "replace_context") {
		t.Fatalf("single-tool Codex args leaked the janitor default tools:\n%s", got)
	}
}

func TestCodexRunHeadlessTaskCapturesFinalTextFromLastMessageFile(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "agent")
	script := "#!/bin/sh\n" +
		"next=0\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$next\" = \"1\" ]; then printf 'PONG' > \"$a\"; next=0; fi\n" +
		"  if [ \"$a\" = \"--output-last-message\" ]; then next=1; fi\n" +
		"done\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	result, err := (&Codex{}).RunHeadlessTask(context.Background(), HeadlessTaskRequest{
		Executable:    scriptPath,
		Model:         "gpt-test",
		Prompt:        "ping",
		WorkDir:       dir,
		MCPServerName: "attn_workflow_result",
		ToolName:      "return_result",
	})
	if err != nil {
		t.Fatalf("RunHeadlessTask error: %v", err)
	}
	if result.Text != "PONG" {
		t.Fatalf("captured text = %q, want PONG", result.Text)
	}
}

func TestCodexParseFinalTextFromStdoutFallback(t *testing.T) {
	stdout := []byte(strings.Join([]string{
		`{"type":"thread.started","thread_id":"t"}`,
		`{"type":"item.completed","item":{"id":"i0","type":"agent_message","text":"first"}}`,
		`{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"FINAL"}}`,
		`{"type":"turn.completed","usage":{}}`,
	}, "\n"))
	if got := parseCodexFinalText(stdout); got != "FINAL" {
		t.Fatalf("parseCodexFinalText = %q, want FINAL", got)
	}
}

func TestParseClaudeFinalText(t *testing.T) {
	t.Run("single result object", func(t *testing.T) {
		got := parseClaudeFinalText([]byte(`{"type":"result","subtype":"success","result":"hello"}`))
		if got != "hello" {
			t.Fatalf("got %q, want hello", got)
		}
	})
	t.Run("stream array last result wins", func(t *testing.T) {
		stdout := []byte(`[{"type":"assistant","message":{"content":[{"type":"text","text":"thinking"}]}},{"type":"result","result":"final-answer"}]`)
		if got := parseClaudeFinalText(stdout); got != "final-answer" {
			t.Fatalf("got %q, want final-answer", got)
		}
	})
	t.Run("stream array falls back to assistant text", func(t *testing.T) {
		stdout := []byte(`[{"type":"assistant","message":{"content":[{"type":"text","text":"only-text"}]}}]`)
		if got := parseClaudeFinalText(stdout); got != "only-text" {
			t.Fatalf("got %q, want only-text", got)
		}
	})
}

func TestParseClaudeResultMeta(t *testing.T) {
	// Shapes match the empirically captured --json-schema envelope (2.1.198).
	t.Run("single result object", func(t *testing.T) {
		meta := parseClaudeResultMeta([]byte(`{"type":"result","result":"{\"verdict\":\"ok\"}","structured_output":{"verdict":"ok"},"total_cost_usd":0.0053,"num_turns":2}`))
		if string(meta.StructuredOutput) != `{"verdict":"ok"}` {
			t.Fatalf("StructuredOutput = %s", meta.StructuredOutput)
		}
		if meta.TotalCostUSD != 0.0053 || meta.NumTurns != 2 {
			t.Fatalf("meta = %+v", meta)
		}
	})
	t.Run("stream array last result wins", func(t *testing.T) {
		stdout := []byte(`[{"type":"system","subtype":"init"},{"type":"assistant","message":{"content":[]}},{"type":"result","structured_output":{"verdict":"ok"},"total_cost_usd":0.5,"num_turns":15}]`)
		meta := parseClaudeResultMeta(stdout)
		if string(meta.StructuredOutput) != `{"verdict":"ok"}` || meta.NumTurns != 15 {
			t.Fatalf("meta = %+v", meta)
		}
	})
	t.Run("no result event yields zero meta", func(t *testing.T) {
		meta := parseClaudeResultMeta([]byte(`[{"type":"system","subtype":"init"}]`))
		if len(meta.StructuredOutput) != 0 || meta.TotalCostUSD != 0 || meta.NumTurns != 0 {
			t.Fatalf("meta = %+v, want zero", meta)
		}
	})
}

func TestClaudeRunHeadlessTaskScopesSingleToolName(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "agent")
	logPath := filepath.Join(dir, "args.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellSingleQuote(logPath) +
		"\nprintf '{\"type\":\"result\",\"result\":\"done\"}\\n'\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	result, err := (&Claude{}).RunHeadlessTask(context.Background(), HeadlessTaskRequest{
		Executable:       scriptPath,
		Model:            "claude-test",
		Prompt:           "answer",
		WorkDir:          dir,
		MCPServerName:    "attn_workflow_result",
		MCPServerCommand: "/tmp/attn",
		ToolName:         "return_result",
		MCPServerArgs:    []string{"_workflow-result-mcp", "--result-file", "/tmp/result"},
	})
	if err != nil {
		t.Fatalf("RunHeadlessTask error: %v", err)
	}
	if result.Text != "done" {
		t.Fatalf("captured text = %q, want done", result.Text)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := string(args)
	if !strings.Contains(got, "mcp__attn_workflow_result__return_result") {
		t.Fatalf("Claude single-tool args missing the prefixed tool:\n%s", got)
	}
	if strings.Contains(got, "read_context") || strings.Contains(got, "replace_context") {
		t.Fatalf("single-tool Claude args leaked the janitor default tools:\n%s", got)
	}
}

func TestClaudeRunHeadlessTaskExcludesNonManagedSettingsWithoutExplicitAuthentication(t *testing.T) {
	for _, name := range []string{
		"ANTHROPIC_API_KEY",
		"CLAUDE_CODE_USE_BEDROCK",
		"CLAUDE_CODE_USE_VERTEX",
		"CLAUDE_CODE_USE_FOUNDRY",
	} {
		t.Setenv(name, "")
	}
	executable, logPath := writeHeadlessArgsRecorder(t)
	_, err := (&Claude{}).RunHeadlessTask(context.Background(), HeadlessTaskRequest{
		Executable: executable,
		Model:      "claude-test",
		Prompt:     "compact",
		WorkDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("RunHeadlessTask error: %v", err)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := string(args)
	for _, want := range []string{
		"--print",
		"--setting-sources",
		"--no-session-persistence",
		// --strict-mcp-config with NO --mcp-config loads zero MCP servers; --setting-sources ""
		// alone does not stop claude.ai connectors (the 2026-07-02 classifier failure).
		"--strict-mcp-config",
		"--disable-slash-commands",
		"--no-chrome",
		"--allowedTools",
		"Read,Write,Edit,Grep,Glob",
		"--permission-mode",
		"dontAsk",
		"claude-test",
		"compact",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Claude args missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "--setting-sources\n\n--strict-mcp-config") {
		t.Fatalf("Claude args did not pass an empty setting source list:\n%s", got)
	}
	for _, forbidden := range []string{
		"--mcp-config",
		"--tools",
		"mcp__attn_context__read_context,mcp__attn_context__replace_context",
		"--bare",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("Claude managed-auth args unexpectedly contained %q:\n%s", forbidden, got)
		}
	}
}

func TestClaudeRunHeadlessTaskUsesBareModeWithExplicitAuthentication(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	executable, logPath := writeHeadlessArgsRecorder(t)
	_, err := (&Claude{}).RunHeadlessTask(context.Background(), HeadlessTaskRequest{
		Executable: executable,
		Model:      "claude-test",
		Prompt:     "compact",
		WorkDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("RunHeadlessTask error: %v", err)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := string(args)
	if !strings.Contains(got, "--bare") {
		t.Fatalf("Claude explicit-auth args missing --bare:\n%s", got)
	}
	if strings.Contains(got, "--safe-mode") {
		t.Fatalf("Claude explicit-auth args unexpectedly contained --safe-mode:\n%s", got)
	}
}

func TestRunHeadlessCommandUsesMinimalEnvironmentAndDiscardsOutput(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "env.log")
	scriptPath := filepath.Join(dir, "agent")
	script := "#!/bin/sh\n/usr/bin/env > " + shellSingleQuote(logPath) + "\nprintf 'workspace context secret\\n'\nprintf 'stderr secret\\n' >&2\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	profileDir := filepath.Join(dir, "attn-profile")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatalf("create profile dir: %v", err)
	}
	wrapperPath := filepath.Join(profileDir, "attn")
	if err := os.WriteFile(wrapperPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write active attn wrapper: %v", err)
	}
	staleDir := filepath.Join(dir, "stale-attn")
	t.Setenv("ATTN_WRAPPER_PATH", wrapperPath)
	t.Setenv("PATH", strings.Join([]string{staleDir, profileDir, staleDir}, string(os.PathListSeparator)))
	t.Setenv("ATTN_SESSION_ID", "session-secret")
	t.Setenv("CODEX_THREAD_ID", "thread-secret")
	t.Setenv("UNRELATED_SECRET", "secret")
	t.Setenv("ANTHROPIC_API_KEY", "auth-kept")
	t.Setenv("OPENAI_API_KEY", "other-provider-secret")

	result, _, err := runHeadlessCommand(context.Background(), scriptPath, nil, dir, "claude")
	if err != nil {
		t.Fatalf("runHeadlessCommand error: %v", err)
	}
	if result.Diagnostics != "" {
		t.Fatalf("diagnostics = %q, want empty", result.Diagnostics)
	}
	envBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read environment: %v", err)
	}
	env := string(envBytes)
	for _, forbidden := range []string{"ATTN_SESSION_ID=", "CODEX_THREAD_ID=", "UNRELATED_SECRET="} {
		if strings.Contains(env, forbidden) {
			t.Fatalf("headless environment retained %s:\n%s", forbidden, env)
		}
	}
	if !strings.Contains(env, "ANTHROPIC_API_KEY=auth-kept") {
		t.Fatalf("headless environment dropped provider authentication:\n%s", env)
	}
	if !strings.Contains(env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1") {
		t.Fatalf("Claude environment did not disable auto-memory:\n%s", env)
	}
	if strings.Contains(env, "OPENAI_API_KEY=") {
		t.Fatalf("Claude environment retained Codex provider authentication:\n%s", env)
	}
	wantPath := "PATH=" + strings.Join([]string{profileDir, staleDir}, string(os.PathListSeparator))
	if !strings.Contains(env, wantPath+"\n") {
		t.Fatalf("headless PATH did not select active profile wrapper first: want %q in:\n%s", wantPath, env)
	}
}

func TestRunHeadlessCommandClassifiesFailureWithoutLeakingOutput(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "agent")
	script := "#!/bin/sh\nprintf 'authentication_failed workspace context secret\\n'\nprintf 'real stderr cause\\n' >&2\nexit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	result, _, err := runHeadlessCommand(context.Background(), scriptPath, nil, dir, "claude")
	if err == nil {
		t.Fatal("runHeadlessCommand unexpectedly succeeded")
	}
	if result.Diagnostics != "headless agent authentication failed" {
		t.Fatalf("diagnostics = %q", result.Diagnostics)
	}
	if strings.Contains(err.Error(), "workspace context secret") {
		t.Fatalf("error leaked child output: %v", err)
	}
	if want := "stderr: real stderr cause\nstdout: authentication_failed workspace context secret"; result.FailureOutput != want {
		t.Fatalf("failure output = %q, want %q", result.FailureOutput, want)
	}
}

func TestRunHeadlessCommandBoundsFailureOutputTail(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "agent")
	script := "#!/bin/sh\nawk 'BEGIN { for (i = 0; i < 512; i++) printf \"noise-%d \", i; print \"fatal: the real cause\" }' >&2\nexit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	result, _, err := runHeadlessCommand(context.Background(), scriptPath, nil, dir, "claude")
	if err == nil {
		t.Fatal("runHeadlessCommand unexpectedly succeeded")
	}
	if !strings.HasPrefix(result.FailureOutput, "stderr: …(truncated) ") {
		t.Fatalf("failure output not marked truncated: %q", result.FailureOutput[:40])
	}
	if !strings.HasSuffix(result.FailureOutput, "fatal: the real cause") {
		t.Fatalf("failure output lost the tail: %q", result.FailureOutput[len(result.FailureOutput)-60:])
	}
	if len(result.FailureOutput) > headlessFailureOutputLimit+64 {
		t.Fatalf("failure output length = %d, want <= limit + marker", len(result.FailureOutput))
	}
}

func TestClaudeHeadlessTaskAvailabilitySupportsManagedAuthentication(t *testing.T) {
	for _, name := range []string{
		"ANTHROPIC_API_KEY",
		"CLAUDE_CODE_USE_BEDROCK",
		"CLAUDE_CODE_USE_VERTEX",
		"CLAUDE_CODE_USE_FOUNDRY",
	} {
		t.Setenv(name, "")
	}
	if available, reason := (&Claude{}).HeadlessTaskAvailability(); !available || reason != "" {
		t.Fatalf("availability = %t, reason = %q", available, reason)
	}
	if got := claudeHeadlessIsolationArgs(); len(got) != 2 || got[0] != "--setting-sources" || got[1] != "" {
		t.Fatalf("isolation args = %#v, want empty --setting-sources", got)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	if got := claudeHeadlessIsolationArgs(); len(got) != 1 || got[0] != "--bare" {
		t.Fatalf("isolation args = %#v, want --bare", got)
	}
}

func TestCodexHeadlessArgsWidensWritableRootsAdditively(t *testing.T) {
	t.Run("narration widens", func(t *testing.T) {
		args := codexHeadlessArgs(HeadlessTaskRequest{
			Model:              "gpt-test",
			Prompt:             "narrate",
			ExtraWritableRoots: []string{"/notebook/root", "  ", "/notebook/raw"},
		}, 0)
		assertContainsAll(t, "codex narrate args", args,
			"--add-dir\x00/notebook/root", "--add-dir\x00/notebook/raw",
			"workspace-write", "features.apps=false", "narrate")
		joined := strings.Join(args, "\x00")
		if strings.Count(joined, "--add-dir") != 2 {
			t.Fatalf("expected exactly 2 --add-dir entries, got:\n%v", args)
		}
		addDirIdx := strings.Index(joined, "--add-dir")
		lockIdx := strings.Index(joined, "features.apps=false")
		promptIdx := strings.LastIndex(joined, "narrate")
		if !(addDirIdx < lockIdx && lockIdx < promptIdx) {
			t.Fatalf("arg ordering wrong (add-dir=%d lock=%d prompt=%d):\n%v", addDirIdx, lockIdx, promptIdx, args)
		}
	})

	t.Run("keeper compaction adds nothing", func(t *testing.T) {
		args := codexHeadlessArgs(HeadlessTaskRequest{Model: "gpt-test", Prompt: "compact"}, 0)
		assertContainsNone(t, "codex compaction args", args, "--add-dir")
	})
}

func TestClaudeHeadlessArgsIgnoreWritableRoots(t *testing.T) {
	withRoots := claudeHeadlessArgs(HeadlessTaskRequest{
		Model:              "claude-test",
		Prompt:             "narrate",
		AllowedTools:       []string{"Read", "Write", "Edit", "Grep", "Glob", "Bash"},
		ExtraWritableRoots: []string{"/notebook/root"},
	})
	assertContainsNone(t, "claude args", withRoots, "--add-dir", "/notebook/root")
	assertContainsAll(t, "claude args", withRoots,
		"Read,Write,Edit,Grep,Glob,Bash",
		"--permission-mode", "dontAsk", "claude-test", "narrate")
}

func TestClaudeRunHeadlessTaskLeadsFailureOutputWithResultText(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "agent")
	script := "#!/bin/sh\n" +
		"printf '[{\"type\":\"system\",\"subtype\":\"init\"},{\"type\":\"result\",\"is_error\":true,\"result\":\"This model may not exist\"}]\\n'\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	result, err := (&Claude{}).RunHeadlessTask(context.Background(), HeadlessTaskRequest{
		Executable: scriptPath,
		Model:      "claude-test",
		Prompt:     "judge",
		WorkDir:    dir,
	})
	if err == nil {
		t.Fatal("RunHeadlessTask unexpectedly succeeded")
	}
	if !strings.HasPrefix(result.FailureOutput, "result: This model may not exist") {
		t.Fatalf("failure output does not lead with the result text: %q", result.FailureOutput)
	}
	if !strings.Contains(result.FailureOutput, "stdout: ") {
		t.Fatalf("failure output lost the raw tail: %q", result.FailureOutput)
	}
}
