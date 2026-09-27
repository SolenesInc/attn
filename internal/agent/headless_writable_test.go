package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestHeadlessToolServerRunsWidenOnlyForWorkspaceWrite(t *testing.T) {
	for _, sandbox := range []string{"", "read-only", "danger-full-access", "workspace-write"} {
		t.Run(sandbox, func(t *testing.T) {
			request := HeadlessTaskRequest{
				Model:            "model",
				Prompt:           "build it",
				WorkDir:          "/tmp/work",
				CWD:              "/tmp/tree",
				Sandbox:          sandbox,
				MCPServerName:    "attn_workflow_result",
				MCPServerCommand: "/tmp/attn",
				ToolName:         "return_result",
				ExtraMCPServers:  []MCPServerSpec{{Name: "session_tools", Command: "/tmp/tools", EnabledTools: []string{"do_thing"}}},
			}
			writable := sandbox == "workspace-write"

			codex := buildCodexHeadlessArgs(request, "", 0)
			mode := codex[slices.Index(codex, "--sandbox")+1]
			if (mode == "workspace-write") != writable || (mode != "read-only" && !writable) {
				t.Errorf("codex ran sandbox %q under %q", mode, sandbox)
			}
			if slices.Contains(codex, "features.shell_tool=true") != writable || slices.Contains(codex, "features.shell_tool=false") == writable {
				t.Errorf("codex shell tool under %q: %q", sandbox, codex)
			}
			if !slices.Contains(codex, `approval_policy="never"`) || slices.Contains(codex, "--dangerously-bypass-approvals-and-sandbox") {
				t.Errorf("codex under %q escaped its sandbox: %q", sandbox, codex)
			}

			claude, err := buildClaudeHeadlessArgs(request)
			if err != nil {
				t.Fatal(err)
			}
			tools := strings.Split(claude[slices.Index(claude, "--allowedTools")+1], ",")
			for _, native := range []string{"Edit", "Write", "MultiEdit", "Bash"} {
				if slices.Contains(tools, native) != writable {
					t.Errorf("claude under %q allowed %v", sandbox, tools)
				}
			}
			if !slices.Contains(tools, "mcp__attn_workflow_result__return_result") || !slices.Contains(tools, "mcp__session_tools__do_thing") {
				t.Errorf("claude under %q lost its tool servers' tools: %v", sandbox, tools)
			}
			if slices.Contains(claude, "--dangerously-skip-permissions") || !slices.Contains(claude, "dontAsk") {
				t.Errorf("claude under %q escaped its permissions: %q", sandbox, claude)
			}
		})
	}
}

func TestCodexRunHeadlessTaskUsesCWDAsProcessDir(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	scriptPath := filepath.Join(dir, "agent")
	pwdLog := filepath.Join(dir, "pwd.log")
	script := "#!/bin/sh\npwd > '" + pwdLog + "'\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	_, err := (&Codex{}).RunHeadlessTask(context.Background(), HeadlessTaskRequest{
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
	got, err := os.ReadFile(pwdLog)
	if err != nil {
		t.Fatalf("read pwd: %v", err)
	}
	want, _ := filepath.EvalSymlinks(cwd)
	if strings.TrimSpace(string(got)) != want {
		t.Fatalf("process cwd = %q, want %q", got, want)
	}
}
