package agent

import (
	"slices"
	"testing"
)

func TestClaudeBuildCommand_DeniesListAgentsKeepsSendMessage(t *testing.T) {
	cmd := (&Claude{}).BuildCommand(SpawnOpts{
		SessionID:  "sess-1",
		CWD:        "/tmp/project",
		Executable: "claude",
	})
	i := slices.Index(cmd.Args, "--disallowed-tools")
	if i < 0 {
		t.Fatalf("args = %#v, want --disallowed-tools", cmd.Args)
	}
	if got := cmd.Args[i+1:]; len(got) != 1 || got[0] != "ListAgents" {
		t.Fatalf("--disallowed-tools = %#v, want one element ListAgents", got)
	}
	if slices.Contains(cmd.Args, "SendMessage") {
		t.Fatalf("args = %#v, want SendMessage not denied", cmd.Args)
	}
}

// Everything after "--" is the initial prompt, so a flag appended past it would
// be typed at the agent instead of read by it.
func TestClaudeBuildCommand_DenyPrecedesInitialPrompt(t *testing.T) {
	cmd := (&Claude{}).BuildCommand(SpawnOpts{
		SessionID:     "sess-1",
		CWD:           "/tmp/project",
		Executable:    "claude",
		InitialPrompt: "get to work",
	})
	deny := slices.Index(cmd.Args, "--disallowed-tools")
	sep := slices.Index(cmd.Args, "--")
	if deny < 0 || sep < 0 || deny > sep {
		t.Fatalf("args = %#v, want --disallowed-tools before the -- prompt separator", cmd.Args)
	}
}

func TestClaudeBuildCommand_PeerMessagingEnvRestoresTools(t *testing.T) {
	t.Setenv("ATTN_CLAUDE_PEER_MESSAGING", "1")
	cmd := (&Claude{}).BuildCommand(SpawnOpts{
		SessionID:  "sess-1",
		CWD:        "/tmp/project",
		Executable: "claude",
	})
	if slices.Contains(cmd.Args, "--disallowed-tools") {
		t.Fatalf("args = %#v, want no --disallowed-tools with ATTN_CLAUDE_PEER_MESSAGING=1", cmd.Args)
	}
}

func TestOtherDriversHaveNoPeerMessagingDeny(t *testing.T) {
	for _, driver := range []Driver{&Codex{}, &Copilot{}} {
		cmd := driver.BuildCommand(SpawnOpts{
			SessionID:  "sess-1",
			CWD:        "/tmp/project",
			Executable: driver.DefaultExecutable(),
		})
		if slices.Contains(cmd.Args, "--disallowed-tools") {
			t.Fatalf("%s args = %#v, want no --disallowed-tools", driver.Name(), cmd.Args)
		}
	}
}
