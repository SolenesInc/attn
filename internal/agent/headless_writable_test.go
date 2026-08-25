package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExecutable(path, script string) error {
	return os.WriteFile(path, []byte(script), 0o755)
}

func testContext() context.Context { return context.Background() }

func readFileTrim(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// sameDir resolves symlinks first: macOS maps /var -> /private/var, which `pwd`
// reports but t.TempDir() does not.
func sameDir(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = a
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = b
	}
	return ra == rb
}

func argvValueAfter(argv []string, flag string) string {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag {
			return argv[i+1]
		}
	}
	return ""
}

func argvHas(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}

func argvHasPair(argv []string, flag, value string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == value {
			return true
		}
	}
	return false
}

func TestJanitorShapedRequestStaysReadOnly(t *testing.T) {
	janitor := HeadlessTaskRequest{
		Model:            "janitor-model",
		Prompt:           "compact",
		WorkDir:          "/tmp/janitor",
		MCPServerName:    "attn_context",
		MCPServerCommand: "/tmp/attn",
		MCPServerArgs: []string{
			"_workspace-context-janitor-mcp",
			"--source-file", "/tmp/janitor/source.md",
			"--candidate-file", "/tmp/janitor/candidate.md",
		},
	}

	codexArgv := buildCodexHeadlessArgs(janitor, "", 0)
	if !argvHasPair(codexArgv, "--sandbox", "read-only") {
		t.Fatalf("janitor codex argv was not read-only: %v", codexArgv)
	}
	if !argvHas(codexArgv, "features.shell_tool=false") {
		t.Fatalf("janitor codex argv enabled the shell tool: %v", codexArgv)
	}
	for _, a := range codexArgv {
		if strings.HasPrefix(a, "mcp_servers.") && !strings.HasPrefix(a, "mcp_servers.attn_context.") {
			t.Fatalf("janitor codex argv leaked an extra mcp server: %q", a)
		}
	}

	claudeArgv, err := buildClaudeHeadlessArgs(janitor)
	if err != nil {
		t.Fatalf("buildClaudeHeadlessArgs: %v", err)
	}
	tools := argvValueAfter(claudeArgv, "--allowedTools")
	if tools != "mcp__attn_context__read_context,mcp__attn_context__replace_context" {
		t.Fatalf("janitor claude allowlist was not the locked MCP tool pair: %q", tools)
	}
	for _, native := range []string{"Edit", "Write", "MultiEdit", "Bash"} {
		if strings.Contains(tools, native) {
			t.Fatalf("janitor claude allowlist leaked native tool %q: %q", native, tools)
		}
	}
}

func TestBuildCodexHeadlessArgsReadOnlyDefault(t *testing.T) {
	argv := buildCodexHeadlessArgs(HeadlessTaskRequest{
		Model:            "gpt-test",
		Prompt:           "compact",
		WorkDir:          "/tmp/work",
		MCPServerName:    "attn_context",
		MCPServerCommand: "/tmp/attn",
		MCPServerArgs:    []string{"_workspace-context-janitor-mcp", "--source-file", "/tmp/source"},
	}, "", 0)

	if !argvHasPair(argv, "--sandbox", "read-only") {
		t.Fatalf("read-only default did not emit --sandbox read-only: %v", argv)
	}
	if argvHas(argv, "workspace-write") {
		t.Fatalf("read-only default leaked workspace-write: %v", argv)
	}
	if !argvHas(argv, "features.shell_tool=false") {
		t.Fatalf("read-only default did not disable the shell tool: %v", argv)
	}
	if argvHas(argv, "features.shell_tool=true") {
		t.Fatalf("read-only default enabled the shell tool: %v", argv)
	}
	if argvHas(argv, "--dangerously-bypass-approvals-and-sandbox") || argvHas(argv, "danger-full-access") {
		t.Fatalf("argv used a sandbox bypass: %v", argv)
	}
	if !argvHas(argv, `approval_policy="never"`) {
		t.Fatalf("argv dropped approval_policy=never: %v", argv)
	}
	if !argvHas(argv, `mcp_servers.attn_context.enabled_tools=["read_context","replace_context"]`) {
		t.Fatalf("argv did not keep the janitor default tools: %v", argv)
	}
	for _, a := range argv {
		if strings.HasPrefix(a, "mcp_servers.") && !strings.HasPrefix(a, "mcp_servers.attn_context.") {
			t.Fatalf("janitor-shaped request leaked an extra mcp server: %q", a)
		}
	}
}

func TestBuildCodexHeadlessArgsWritableEnablesSandboxAndShell(t *testing.T) {
	argv := buildCodexHeadlessArgs(HeadlessTaskRequest{
		Model:            "gpt-test",
		Prompt:           "build it",
		WorkDir:          "/tmp/work",
		CWD:              "/tmp/tree",
		Sandbox:          "workspace-write",
		MCPServerName:    "attn_workflow_result",
		MCPServerCommand: "/tmp/attn",
		ToolName:         "return_result",
		MCPServerArgs:    []string{"_workflow-result-mcp", "--result-file", "/tmp/result"},
		ExtraMCPServers: []MCPServerSpec{{
			Name:         "session_tools",
			Command:      "/tmp/session-mcp",
			Args:         []string{"serve"},
			EnabledTools: []string{"do_thing", "other_thing"},
		}},
	}, "/tmp/work/codex-last.txt", 0)

	if !argvHasPair(argv, "--sandbox", "workspace-write") {
		t.Fatalf("writable did not emit --sandbox workspace-write: %v", argv)
	}
	if !argvHas(argv, "features.shell_tool=true") {
		t.Fatalf("writable did not enable the shell tool: %v", argv)
	}
	if !argvHas(argv, `approval_policy="never"`) {
		t.Fatalf("writable dropped approval_policy=never (sandbox is the boundary): %v", argv)
	}
	if argvHas(argv, "--dangerously-bypass-approvals-and-sandbox") || argvHas(argv, "danger-full-access") {
		t.Fatalf("writable used a sandbox bypass: %v", argv)
	}
	for _, off := range []string{
		"features.unified_exec=false", "features.apps=false", "features.hooks=false",
		"features.plugins=false", "features.browser_use=false", "features.computer_use=false",
		"features.memories=false", "features.multi_agent=false", "features.goals=false",
		"features.standalone_web_search=false",
	} {
		if !argvHas(argv, off) {
			t.Fatalf("writable re-enabled a non-essential feature (missing %q): %v", off, argv)
		}
	}
	if !argvHas(argv, `mcp_servers.session_tools.enabled_tools=["do_thing","other_thing"]`) {
		t.Fatalf("extra MCP server tools not emitted: %v", argv)
	}
	if !argvHas(argv, `mcp_servers.session_tools.command="/tmp/session-mcp"`) {
		t.Fatalf("extra MCP server command not emitted: %v", argv)
	}
	if !argvHas(argv, "mcp_servers.session_tools.required=true") {
		t.Fatalf("extra MCP server not marked required: %v", argv)
	}
	if !argvHas(argv, `mcp_servers.attn_workflow_result.enabled_tools=["return_result"]`) {
		t.Fatalf("primary result sink lost its tool: %v", argv)
	}
}

func TestBuildCodexHeadlessArgsUnknownSandboxFailsClosed(t *testing.T) {
	argv := buildCodexHeadlessArgs(HeadlessTaskRequest{
		Model:   "gpt-test",
		Prompt:  "x",
		Sandbox: "full-access",
	}, "", 0)
	if !argvHasPair(argv, "--sandbox", "read-only") {
		t.Fatalf("unrecognized sandbox value did not fail closed to read-only: %v", argv)
	}
	if !argvHas(argv, "features.shell_tool=false") {
		t.Fatalf("unrecognized sandbox value enabled the shell tool: %v", argv)
	}
}

func TestCodexRunHeadlessTaskUsesCWDAsProcessDir(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	scriptPath := dir + "/agent"
	pwdLog := dir + "/pwd.log"
	script := "#!/bin/sh\npwd > " + shellSingleQuote(pwdLog) + "\n"
	if err := writeExecutable(scriptPath, script); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	_, err := (&Codex{}).RunHeadlessTask(testContext(), HeadlessTaskRequest{
		Executable:    scriptPath,
		Model:         "gpt-test",
		Prompt:        "build",
		WorkDir:       dir,
		CWD:           cwd,
		Sandbox:       "workspace-write",
		MCPServerName: "attn_workflow_result",
		ToolName:      "return_result",
	})
	if err != nil {
		t.Fatalf("RunHeadlessTask error: %v", err)
	}
	got, err := readFileTrim(pwdLog)
	if err != nil {
		t.Fatalf("read pwd: %v", err)
	}
	if !sameDir(got, cwd) {
		t.Fatalf("process cwd = %q, want %q", got, cwd)
	}
}

func TestBuildClaudeHeadlessArgsReadOnlyDefault(t *testing.T) {
	argv, err := buildClaudeHeadlessArgs(HeadlessTaskRequest{
		Model:            "claude-test",
		Prompt:           "compact",
		WorkDir:          "/tmp/work",
		MCPServerName:    "attn_context",
		MCPServerCommand: "/tmp/attn",
		MCPServerArgs:    []string{"_workspace-context-janitor-mcp"},
	})
	if err != nil {
		t.Fatalf("buildClaudeHeadlessArgs: %v", err)
	}
	tools := argvValueAfter(argv, "--allowedTools")
	if tools != "mcp__attn_context__read_context,mcp__attn_context__replace_context" {
		t.Fatalf("read-only allowlist = %q, want the locked MCP tool pair", tools)
	}
	for _, native := range []string{"Edit", "Write", "MultiEdit", "Bash"} {
		if strings.Contains(tools, native) {
			t.Fatalf("read-only allowlist leaked native tool %q: %q", native, tools)
		}
	}
	if argvHas(argv, "--dangerously-skip-permissions") {
		t.Fatalf("read-only used --dangerously-skip-permissions: %v", argv)
	}
}

func TestBuildClaudeHeadlessArgsWritableAddsEditAndBashAndExtraServers(t *testing.T) {
	argv, err := buildClaudeHeadlessArgs(HeadlessTaskRequest{
		Model:            "claude-test",
		Prompt:           "build it",
		WorkDir:          "/tmp/work",
		CWD:              "/tmp/tree",
		Sandbox:          "workspace-write",
		MCPServerName:    "attn_workflow_result",
		MCPServerCommand: "/tmp/attn",
		ToolName:         "return_result",
		ExtraMCPServers: []MCPServerSpec{{
			Name:         "session_tools",
			Command:      "/tmp/session-mcp",
			Args:         []string{"serve"},
			EnabledTools: []string{"do_thing"},
		}},
	})
	if err != nil {
		t.Fatalf("buildClaudeHeadlessArgs: %v", err)
	}
	tools := argvValueAfter(argv, "--allowedTools")
	for _, want := range []string{
		"mcp__attn_workflow_result__return_result",
		"mcp__session_tools__do_thing",
		"Edit", "Write", "MultiEdit", "Bash",
	} {
		if !strings.Contains(tools, want) {
			t.Fatalf("writable allowlist missing %q: %q", want, tools)
		}
	}
	if argvValueAfter(argv, "--tools") != tools {
		t.Fatalf("--tools != --allowedTools: %q vs %q", argvValueAfter(argv, "--tools"), tools)
	}
	cfg := argvValueAfter(argv, "--mcp-config")
	for _, want := range []string{"attn_workflow_result", "session_tools", "/tmp/session-mcp"} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("--mcp-config missing %q: %s", want, cfg)
		}
	}
	if argvHas(argv, "--dangerously-skip-permissions") {
		t.Fatalf("writable used --dangerously-skip-permissions: %v", argv)
	}
}

func TestBuildCodexHeadlessArgsOmitsModelWhenEmpty(t *testing.T) {
	// No Model means codex must NOT receive "-m" at all: an empty "-m" makes codex
	// reject the run as "model is invalid or unavailable".
	argv := buildCodexHeadlessArgs(HeadlessTaskRequest{
		Prompt:           "ping",
		MCPServerName:    "attn_workflow_result",
		MCPServerCommand: "/tmp/attn",
		ToolName:         "return_result",
	}, "", 0)
	if argvHas(argv, "-m") {
		t.Fatalf("empty model still emitted -m: %v", argv)
	}
	for _, a := range argv {
		if a == "" {
			t.Fatalf("empty model produced an empty argv token: %v", argv)
		}
	}
	withModel := buildCodexHeadlessArgs(HeadlessTaskRequest{
		Model:            "gpt-test",
		Prompt:           "ping",
		MCPServerName:    "attn_workflow_result",
		MCPServerCommand: "/tmp/attn",
		ToolName:         "return_result",
	}, "", 0)
	if !argvHasPair(withModel, "-m", "gpt-test") {
		t.Fatalf("non-empty model not pinned with -m: %v", withModel)
	}
}

func TestBuildClaudeHeadlessArgsOmitsModelWhenEmpty(t *testing.T) {
	argv, err := buildClaudeHeadlessArgs(HeadlessTaskRequest{
		Prompt:           "ping",
		MCPServerName:    "attn_workflow_result",
		MCPServerCommand: "/tmp/attn",
		ToolName:         "return_result",
	})
	if err != nil {
		t.Fatalf("buildClaudeHeadlessArgs: %v", err)
	}
	if argvHas(argv, "--model") {
		t.Fatalf("empty model still emitted --model: %v", argv)
	}
	withModel, err := buildClaudeHeadlessArgs(HeadlessTaskRequest{
		Model:            "claude-test",
		Prompt:           "ping",
		MCPServerName:    "attn_workflow_result",
		MCPServerCommand: "/tmp/attn",
		ToolName:         "return_result",
	})
	if err != nil {
		t.Fatalf("buildClaudeHeadlessArgs(withModel): %v", err)
	}
	if !argvHasPair(withModel, "--model", "claude-test") {
		t.Fatalf("non-empty model not pinned with --model: %v", withModel)
	}
}
