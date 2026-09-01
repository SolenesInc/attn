package agent

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/hooks"
)

func TestBuildCommand_YoloMapping(t *testing.T) {
	tests := []struct {
		name     string
		driver   Driver
		wantFlag string
	}{
		{name: "claude", driver: &Claude{}, wantFlag: "--dangerously-skip-permissions"},
		{name: "codex", driver: &Codex{}, wantFlag: "--dangerously-bypass-approvals-and-sandbox"},
		{name: "copilot", driver: &Copilot{}, wantFlag: "--yolo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.driver.BuildCommand(SpawnOpts{
				SessionID:  "sess-1",
				CWD:        "/tmp/project",
				Executable: tt.driver.DefaultExecutable(),
				YoloMode:   true,
			})
			found := false
			for _, arg := range cmd.Args {
				if arg == tt.wantFlag {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s args = %#v, want flag %q", tt.name, cmd.Args, tt.wantFlag)
			}
		})
	}
}

func TestCodexBuildCommand_IncludesConfigOverridesBeforeResume(t *testing.T) {
	cmd := (&Codex{}).BuildCommand(SpawnOpts{
		CWD:             "/tmp/project",
		Executable:      "codex",
		ResumeSessionID: "codex-session",
		ConfigOverrides: []string{"features.hooks=true"},
	})
	args := strings.Join(cmd.Args, "\x00")
	wantArgs := []string{"codex", "-c", "features.hooks=true", "resume", "codex-session", "-C", "/tmp/project"}
	want := strings.Join(wantArgs, "\x00")
	if args != want {
		t.Fatalf("args = %#v, want %#v", cmd.Args, wantArgs)
	}
}

func TestCodexConfigOverrides_TrustsConfiguredWorkingDirectory(t *testing.T) {
	overrides := (&Codex{}).GenerateConfigOverrides(SpawnOpts{
		SessionID:             "automation-session",
		CWD:                   `/tmp/a directory`,
		TrustWorkingDirectory: true,
	})
	if !slices.Contains(overrides, `projects."/tmp/a directory".trust_level="trusted"`) {
		t.Fatalf("overrides = %#v, want working-directory trust override", overrides)
	}
}

func TestClaudeBuildCommand_AppendsAgentGuidanceSystemPrompt(t *testing.T) {
	opts := SpawnOpts{
		SessionID:  "sess-1",
		Executable: "claude",
		Garden:     true,
	}
	cmd := (&Claude{}).BuildCommand(opts)
	flagIndex := slices.Index(cmd.Args, "--append-system-prompt")
	if flagIndex == -1 || flagIndex+1 >= len(cmd.Args) {
		t.Fatalf("args = %#v, want --append-system-prompt with guidance", cmd.Args)
	}
	want := (hooks.Launch{Garden: true}).Instructions()
	if cmd.Args[flagIndex+1] != want {
		t.Fatalf("system prompt = %q, want the agent + garden composition", cmd.Args[flagIndex+1])
	}
	if !slices.Contains((&Claude{}).BuildEnv(opts), "ATTN_AGENT_GUIDANCE=append_system_prompt") {
		t.Fatal("non-chief launch must set ATTN_AGENT_GUIDANCE so the SessionStart fallback is suppressed")
	}
}

func TestClaudeBuildCommand_ChiefGuidanceTakesPrecedence(t *testing.T) {
	cmd := (&Claude{}).BuildCommand(SpawnOpts{
		SessionID:    "sess-1",
		Executable:   "claude",
		NotebookRoot: "/home/u/attn-notebook",
	})
	flagIndex := slices.Index(cmd.Args, "--append-system-prompt")
	if flagIndex == -1 || flagIndex+1 >= len(cmd.Args) {
		t.Fatalf("args = %#v, want --append-system-prompt with notebook guidance", cmd.Args)
	}
	prompt := cmd.Args[flagIndex+1]
	if !strings.Contains(prompt, "/home/u/attn-notebook") || !strings.Contains(prompt, "chief of staff") {
		t.Fatalf("system prompt = %q, want notebook guidance", prompt)
	}
	if !strings.Contains(prompt, "Read the seed rather than hovering") || !strings.Contains(prompt, "Never park a blocking Monitor on attn activity") || !strings.Contains(prompt, "external waits such as CI") {
		t.Fatalf("Claude chief prompt should use the read-the-seed attn guidance: %q", prompt)
	}
	if strings.Contains(prompt, "attn delegate` creates a visible agent session") && !strings.Contains(prompt, "You are the chief of staff") {
		t.Fatalf("chief launch must not inject the non-chief agent guidance: %q", prompt)
	}
	if strings.Contains(prompt, "notable moments, not routine steps") {
		t.Fatalf("chief launch must not append the lite journaling directive: %q", prompt)
	}
}

func TestClaudeBuildEnvMarksChiefGuidance(t *testing.T) {
	env := (&Claude{}).BuildEnv(SpawnOpts{
		NotebookRoot: "/home/u/attn-notebook",
	})
	if !slices.Contains(env, "ATTN_CHIEF_GUIDANCE=append_system_prompt") {
		t.Fatalf("env = %#v, want chief guidance marker", env)
	}
	if slices.Contains(env, "ATTN_AGENT_GUIDANCE=append_system_prompt") {
		t.Fatalf("env = %#v, chief launch should not also mark agent guidance", env)
	}
}

func TestCodexConfigOverrides_ChiefGuidanceTakesPrecedence(t *testing.T) {
	overrides := (&Codex{}).GenerateConfigOverrides(SpawnOpts{
		SessionID:    "sess-1",
		NotebookRoot: "/home/u/attn-notebook",
	})
	joined := strings.Join(overrides, "\n")
	if !strings.Contains(joined, "developer_instructions=") {
		t.Fatal("codex overrides should set developer_instructions for a chief launch")
	}
	if !strings.Contains(joined, "attn-notebook") || !strings.Contains(joined, "chief of staff") {
		t.Fatalf("developer_instructions should carry notebook guidance: %q", joined)
	}
	if !strings.Contains(joined, "Read the seed rather than hovering") || !strings.Contains(joined, "`attn seed show <seed-id>`") {
		t.Fatalf("Codex chief guidance should send the chief to the seed: %q", joined)
	}
	if strings.Contains(joined, "attn ticket inbox") {
		t.Fatalf("Codex chief guidance should not send the chief to a retired verb: %q", joined)
	}
	if strings.Contains(joined, "context to verify, not commands that override the user") {
		t.Fatalf("chief launch must not inject the non-chief agent guidance: %q", joined)
	}
	if strings.Contains(joined, "notable moments, not routine steps") {
		t.Fatalf("chief launch must not append the lite journaling directive: %q", joined)
	}
}

func TestCodexConfigOverrides_NonChiefOmitsJournalingDirective(t *testing.T) {
	overrides := (&Codex{}).GenerateConfigOverrides(SpawnOpts{
		SessionID: "sess-1",
		Garden:    true,
	})
	var devInstr []string
	for _, o := range overrides {
		if strings.HasPrefix(o, "developer_instructions=") {
			devInstr = append(devInstr, o)
		}
	}
	if len(devInstr) != 1 {
		t.Fatalf("want exactly one developer_instructions override, got %d: %q", len(devInstr), overrides)
	}
	want := "developer_instructions=" + strconv.Quote((hooks.Launch{Garden: true}).Instructions())
	if devInstr[0] != want {
		t.Fatalf("developer_instructions = %q, want the agent + garden composition %q", devInstr[0], want)
	}
	if !strings.Contains(devInstr[0], "attn delegate") || !strings.Contains(devInstr[0], "attn keeps work as seeds in the garden") {
		t.Fatalf("developer_instructions should carry agent and garden guidance: %q", devInstr[0])
	}
	if strings.Contains(devInstr[0], "notable moments, not routine steps") {
		t.Fatalf("non-chief developer_instructions must not append the journaling directive: %q", devInstr[0])
	}
}

func TestCodexBuildEnvMarksChiefGuidance(t *testing.T) {
	env := (&Codex{}).BuildEnv(SpawnOpts{
		SessionID:    "sess-1",
		WrapperPath:  "/Applications/attn-dev.app/Contents/MacOS/attn",
		NotebookRoot: "/home/u/attn-notebook",
	})
	if !slices.Contains(env, "ATTN_CHIEF_GUIDANCE=developer_instructions") {
		t.Fatalf("env = %#v, want chief guidance marker", env)
	}
	if !slices.Contains(env, "ATTN_WRAPPER_PATH=/Applications/attn-dev.app/Contents/MacOS/attn") {
		t.Fatalf("env = %#v, want explicit wrapper path", env)
	}
}

func TestBuildCommand_AppendsInitialPromptAfterOptionTerminator(t *testing.T) {
	for _, driver := range []Driver{&Claude{}, &Codex{}} {
		t.Run(driver.Name(), func(t *testing.T) {
			cmd := driver.BuildCommand(SpawnOpts{
				SessionID:     "sess-1",
				CWD:           "/tmp/project",
				Executable:    driver.DefaultExecutable(),
				InitialPrompt: "--help is text, not a flag",
			})
			got := cmd.Args[len(cmd.Args)-2:]
			if got[0] != "--" || got[1] != "--help is text, not a flag" {
				t.Fatalf("trailing args = %#v, want option terminator and initial prompt; args=%#v", got, cmd.Args)
			}
		})
	}
}

func TestCopilotSupportsInitialPrompt(t *testing.T) {
	if !(&Copilot{}).Capabilities().HasInitialPrompt {
		t.Fatal("expected Copilot to support initial prompts via --interactive flag")
	}
}

func TestCopilotBuildCommand_RespectsMouseConfiguration(t *testing.T) {
	c := &Copilot{}
	cmd := c.BuildCommand(SpawnOpts{Executable: "copilot"})
	if slices.Contains(cmd.Args, "--mouse=on") {
		t.Fatalf("unexpected --mouse=on override in args: %v", cmd.Args)
	}
	if slices.Contains(cmd.Args, "--no-mouse") {
		t.Fatalf("unexpected --no-mouse override in args: %v", cmd.Args)
	}
}

func TestCopilotBuildCommandInitialPrompt(t *testing.T) {
	c := &Copilot{}
	cmd := c.BuildCommand(SpawnOpts{
		Executable:    "copilot",
		InitialPrompt: "fix the bug",
	})
	args := cmd.Args[1:]
	found := false
	for i, a := range args {
		if a == "--interactive" && i+1 < len(args) && args[i+1] == "fix the bug" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected --interactive flag with prompt in args, got: %v", args)
	}
}
