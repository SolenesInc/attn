package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/hooks"
)

func copilotEnvValue(env []string, key string) string {
	for _, entry := range env {
		if after, ok := strings.CutPrefix(entry, key+"="); ok {
			return after
		}
	}
	return ""
}

func TestCopilotGenerateInstructionsFileCarriesLaunchGuidance(t *testing.T) {
	name, content := (&Copilot{}).GenerateInstructionsFile(SpawnOpts{
		SessionID:              "s",
		Garden:                 true,
		SelfReportPullRequests: true,
	})

	// Copilot reads *.instructions.md from an extra dir and nothing else.
	if !strings.HasSuffix(name, ".instructions.md") {
		t.Fatalf("instructions file %q is not a name copilot loads", name)
	}
	if !strings.Contains(content, hooks.GardenGuidance) {
		t.Fatalf("copilot launch instructions dropped the garden block:\n%s", content)
	}
	if !strings.Contains(content, hooks.PullRequestSelfReportGuidance) {
		t.Fatalf("copilot launch instructions dropped the pull request block:\n%s", content)
	}
	if strings.HasPrefix(strings.TrimSpace(content), "---") {
		t.Fatalf("frontmatter is inlined verbatim and scopes the file away:\n%s", content)
	}

	if _, empty := (&Copilot{}).GenerateInstructionsFile(SpawnOpts{SessionID: "s"}); empty != "" {
		t.Fatalf("a launch with nothing to say still wrote instructions:\n%s", empty)
	}
}

func TestCopilotBuildEnvPointsCopilotAtTheInstructionsDir(t *testing.T) {
	t.Setenv(copilotInstructionsDirsEnv, "")

	env := (&Copilot{}).BuildEnv(SpawnOpts{SessionID: "sess-1", InstructionsDir: "/tmp/attn-instructions-sess-1"})
	if got := copilotEnvValue(env, copilotInstructionsDirsEnv); got != "/tmp/attn-instructions-sess-1" {
		t.Fatalf("%s = %q", copilotInstructionsDirsEnv, got)
	}
	// `attn pr record` resolves the session from the environment; copilot has no hooks.
	if got := copilotEnvValue(env, "ATTN_SESSION_ID"); got != "sess-1" {
		t.Fatalf("ATTN_SESSION_ID = %q", got)
	}

	bare := (&Copilot{}).BuildEnv(SpawnOpts{SessionID: "sess-1"})
	if slices.ContainsFunc(bare, func(e string) bool { return strings.HasPrefix(e, copilotInstructionsDirsEnv+"=") }) {
		t.Fatalf("a launch with no instructions dir still set the var: %v", bare)
	}
}

func TestCopilotInstructionsDirsKeepsTheUsersOwn(t *testing.T) {
	tests := []struct {
		name      string
		inherited string
		dir       string
		want      string
	}{
		{"appended last", "/home/me/instr, /work/instr", "/tmp/attn", "/home/me/instr,/work/instr,/tmp/attn"},
		{"already listed", "/tmp/attn,/home/me/instr", "/tmp/attn", "/home/me/instr,/tmp/attn"},
		{"nothing inherited", "", "/tmp/attn", "/tmp/attn"},
		{"no attn dir", "/home/me/instr", "", "/home/me/instr"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := copilotInstructionsDirs(tc.inherited, tc.dir); got != tc.want {
				t.Fatalf("copilotInstructionsDirs(%q, %q) = %q, want %q", tc.inherited, tc.dir, got, tc.want)
			}
		})
	}
}

func TestEveryDriverCarriesThePullRequestFlag(t *testing.T) {
	on := SpawnOpts{SessionID: "s", Executable: "claude", Garden: true, SelfReportPullRequests: true}
	off := SpawnOpts{SessionID: "s", Executable: "claude", Garden: true}

	guidance := map[string][2]string{
		"claude": {
			argvValueAfter((&Claude{}).BuildCommand(on).Args, "--append-system-prompt"),
			argvValueAfter((&Claude{}).BuildCommand(off).Args, "--append-system-prompt"),
		},
		"codex": {
			strings.Join((&Codex{}).GenerateConfigOverrides(on), "\n"),
			strings.Join((&Codex{}).GenerateConfigOverrides(off), "\n"),
		},
	}
	_, copilotOn := (&Copilot{}).GenerateInstructionsFile(on)
	_, copilotOff := (&Copilot{}).GenerateInstructionsFile(off)
	guidance["copilot"] = [2]string{copilotOn, copilotOff}

	// Each driver assembles its own guidance, so the flag has to survive every route.
	for name, pair := range guidance {
		if !strings.Contains(pair[0], "attn pr record") {
			t.Fatalf("%s dropped the pull request block on the way to its guidance:\n%s", name, pair[0])
		}
		if strings.Contains(pair[1], "attn pr record") {
			t.Fatalf("%s carried the pull request block without the flag:\n%s", name, pair[1])
		}
	}
}
