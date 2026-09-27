package agent

import (
	"os/exec"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

type testDriver struct {
	name string
	caps Capabilities
}

func (d testDriver) Name() string                               { return d.name }
func (d testDriver) DisplayName() string                        { return d.name }
func (d testDriver) DefaultExecutable() string                  { return d.name }
func (d testDriver) ExecutableEnvVar() string                   { return "" }
func (d testDriver) ResolveExecutable(configured string) string { return configured }
func (d testDriver) BuildCommand(opts SpawnOpts) *exec.Cmd      { return exec.Command("true") }
func (d testDriver) BuildEnv(opts SpawnOpts) []string           { return nil }
func (d testDriver) Capabilities() Capabilities                 { return d.caps }

func TestRecoveredRunningSessionState(t *testing.T) {
	plain := testDriver{name: "nopolicy", caps: Capabilities{HasTranscript: true}}
	cases := []struct {
		driver   Driver
		ptyState string
		want     protocol.SessionState
		kept     bool
	}{
		{plain, protocol.StateWaitingInput, protocol.SessionStateWaitingInput, true},
		{plain, protocol.StateWorking, "", false},
		{Get("copilot"), protocol.StatePendingApproval, protocol.SessionStatePendingApproval, true},
		{Get("codex"), protocol.StateWaitingInput, "", false},
		{Get("codex"), protocol.StatePendingApproval, "", false},
		{Get("claude"), protocol.StateWorking, "", false},
	}
	for _, tc := range cases {
		got, kept := RecoveredRunningSessionState(tc.driver, tc.ptyState)
		if kept != tc.kept || (kept && got != tc.want) {
			t.Errorf("%s recovering a PTY in %s = %s (kept=%v), want %s (kept=%v)", tc.driver.Name(), tc.ptyState, got, kept, tc.want, tc.kept)
		}
	}
}
