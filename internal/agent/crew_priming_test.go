package agent

import (
	"strings"
	"testing"
)

const crewBlock = "You are **Trellis**, a crew member of this attn home."

func TestBuiltinAgentsCarryTheCrewPriming(t *testing.T) {
	opts := SpawnOpts{SessionID: "s", Executable: "claude", CrewPriming: crewBlock}

	claude := argvValueAfter((&Claude{}).BuildCommand(opts).Args, "--append-system-prompt")
	if !strings.Contains(claude, crewBlock) {
		t.Fatalf("claude launch dropped the crew priming: %q", claude)
	}

	codex := strings.Join((&Codex{}).GenerateConfigOverrides(SpawnOpts{SessionID: "s", CrewPriming: crewBlock}), "\n")
	if !strings.Contains(codex, "crew member of this attn home") {
		t.Fatalf("codex launch dropped the crew priming: %q", codex)
	}

	bare := argvValueAfter((&Claude{}).BuildCommand(SpawnOpts{SessionID: "s", Executable: "claude"}).Args, "--append-system-prompt")
	if strings.Contains(bare, "crew member of this attn home") {
		t.Fatalf("a session nobody woke as a member was primed anyway: %q", bare)
	}
}

func TestAwarenessDirsAreReachableFromTheLaunch(t *testing.T) {
	opts := SpawnOpts{
		SessionID:     "s",
		Executable:    "claude",
		CWD:           "/work",
		AwarenessDirs: []string{"/homes/trellis", "/projects/pi"},
	}

	claude := strings.Join((&Claude{}).BuildCommand(opts).Args, "\x00")
	for _, want := range []string{"--add-dir\x00/homes/trellis", "--add-dir\x00/projects/pi"} {
		if !strings.Contains(claude, want) {
			t.Errorf("claude launch is missing %q:\n%s", want, claude)
		}
	}

	codexOpts := opts
	codexOpts.Executable = "codex"
	codex := strings.Join((&Codex{}).BuildCommand(codexOpts).Args, "\x00")
	for _, want := range []string{"--add-dir\x00/homes/trellis", "--add-dir\x00/projects/pi"} {
		if !strings.Contains(codex, want) {
			t.Errorf("codex launch is missing %q:\n%s", want, codex)
		}
	}

	plain := strings.Join((&Claude{}).BuildCommand(SpawnOpts{SessionID: "s", Executable: "claude"}).Args, "\x00")
	if strings.Contains(plain, "--add-dir") {
		t.Errorf("a launch with no awareness dirs widened its reach anyway:\n%s", plain)
	}
}
