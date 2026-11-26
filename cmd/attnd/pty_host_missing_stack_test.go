package main_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestANewTerminalOnAMachineWithNoHostIsRefusedWithHowToGetTheHost(t *testing.T) {
	t.Parallel()
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	home.UseSharedHostBinary(filepath.Join(home.Dir, "no-host"))
	outpost.UseSharedHostBinary(filepath.Join(outpost.Dir, "no-host"))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	for _, machine := range []struct {
		name, remedy string
		host         *string
		stack        *testworld.Stack
	}{
		{name: "the home", remedy: "Reinstall attn", stack: home},
		{name: "an outpost", remedy: "Update this outpost from its home", host: &pair.Host, stack: outpost},
	} {
		result, _, _ := home.RequestSpawn(pair.App, fakeagent.Harness(protocol.SessionAgentShell), machine.stack.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = machine.host })
		refusal := protocol.Deref(result.Error)
		if result.Success || !strings.Contains(refusal, "shared terminal host is unavailable") || !strings.Contains(refusal, machine.remedy) {
			t.Errorf("a new terminal on %s with no host answered %+v, want a refusal that says %q", machine.name, result, machine.remedy)
		}
		t.Logf("%s refused: %s", machine.name, refusal)
	}
}
