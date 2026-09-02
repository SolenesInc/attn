package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/hooks"
)

// workflowGuidanceMarker appears nowhere in argv, hook commands, or the agent guidance, so its presence means the workflow block was injected.
const workflowGuidanceMarker = "hypercode"

func TestClaudeBuildCommand_GatesWorkflowGuidance(t *testing.T) {
	off := (&Claude{}).BuildCommand(SpawnOpts{SessionID: "s", Executable: "claude", Garden: true})
	if !slices.Contains(off.Args, "--append-system-prompt") {
		t.Fatalf("home launch should append the garden block: %v", off.Args)
	}
	offPrompt := argvValueAfter(off.Args, "--append-system-prompt")
	if !strings.Contains(offPrompt, hooks.GardenGuidance) {
		t.Fatalf("bare launch system prompt missing the garden block: %q", offPrompt)
	}
	if strings.Contains(offPrompt, workflowGuidanceMarker) {
		t.Fatalf("bare launch leaked workflow guidance: %q", offPrompt)
	}

	on := (&Claude{}).BuildCommand(SpawnOpts{SessionID: "s", Executable: "claude", InjectWorkflowGuidance: true})
	prompt := argvValueAfter(on.Args, "--append-system-prompt")
	if !strings.Contains(prompt, workflowGuidanceMarker) {
		t.Fatalf("enabled launch missing workflow guidance: %q", prompt)
	}

	if !strings.Contains(prompt, hooks.AgentGuidance) {
		t.Fatalf("enabled launch dropped the agent guidance: %q", prompt)
	}
}

func TestCodexGenerateConfigOverrides_GatesWorkflowGuidance(t *testing.T) {
	off := strings.Join((&Codex{}).GenerateConfigOverrides(SpawnOpts{SessionID: "s"}), "\n")
	if strings.Contains(off, workflowGuidanceMarker) {
		t.Fatalf("disabled codex overrides leaked workflow guidance: %q", off)
	}

	on := strings.Join((&Codex{}).GenerateConfigOverrides(SpawnOpts{SessionID: "s", InjectWorkflowGuidance: true}), "\n")
	if !strings.Contains(on, "developer_instructions=") || !strings.Contains(on, workflowGuidanceMarker) {
		t.Fatalf("enabled codex overrides missing workflow guidance: %q", on)
	}
}

func TestHeadlessSubagentArgvCarriesNoWorkflowGuidance(t *testing.T) {
	req := HeadlessTaskRequest{
		Model:            "gpt-test",
		Prompt:           "do the work",
		WorkDir:          "/tmp/work",
		CWD:              "/tmp/tree",
		Sandbox:          "workspace-write",
		MCPServerName:    "attn_workflow_result",
		MCPServerCommand: "/tmp/attn",
		ToolName:         "return_result",
		MCPServerArgs:    []string{"_workflow-result-mcp", "--result-file", "/tmp/result"},
	}

	codexArgv := buildCodexHeadlessArgs(req, "/tmp/work/last.txt", 0)
	claudeArgv, err := buildClaudeHeadlessArgs(req)
	if err != nil {
		t.Fatalf("buildClaudeHeadlessArgs: %v", err)
	}

	for name, argv := range map[string][]string{"codex": codexArgv, "claude": claudeArgv} {
		joined := strings.Join(argv, "\x00")
		if strings.Contains(joined, workflowGuidanceMarker) {
			t.Fatalf("%s headless argv carried workflow guidance: %v", name, argv)
		}
		if slices.Contains(argv, "--append-system-prompt") {
			t.Fatalf("%s headless argv used --append-system-prompt: %v", name, argv)
		}
		if strings.Contains(joined, "developer_instructions=") {
			t.Fatalf("%s headless argv injected developer_instructions: %v", name, argv)
		}
	}
}
