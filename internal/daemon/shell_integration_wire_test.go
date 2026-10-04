package daemon_test

import (
	"os/exec"
	"testing"
)

func TestShellPanesRecordEachCommandAsABlockUnlessTheUserOptsOut(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	for _, optOut := range []bool{false, true} {
		name := map[bool]string{false: "integrated", true: "opted out"}[optOut]
		t.Run(name, func(t *testing.T) {
			w := &world{World: prepareWorld(t)}
			t.Setenv("SHELL", bash)
			if optOut {
				t.Setenv("ATTN_NO_SHELL_INTEGRATION", "1")
			}
			w.start()
			app := w.App()
			shell := w.Spawn(app, shellHarness, w.Path("shop"))
			for _, line := range []string{"/bin/echo attn-integration-probe", "false", "echo probed-$((6*7))"} {
				app.TypeLine(shell, line)
			}
			app.AwaitScreen(shell, "probed-42")

			attached := kittyAttach(transportPeer(w), w.Terminal(shell))
			exits := map[string]int{}
			if attached.Snapshot != nil {
				for _, block := range attached.Snapshot.Blocks {
					if !block.Pending && block.Command != nil && block.ExitCode != nil {
						exits[*block.Command] = *block.ExitCode
					}
				}
			}
			want := map[string]int{"/bin/echo attn-integration-probe": 0, "false": 1}
			if optOut {
				want = map[string]int{}
			}
			for command, code := range want {
				if got, ok := exits[command]; !ok || got != code {
					t.Errorf("the pane's blocks record %v, want %q exiting %d", exits, command, code)
				}
			}
			if optOut && len(exits) != 0 {
				t.Errorf("a pane that opted out recorded blocks %v, want none", exits)
			}
			exitShells(app, shell)
		})
	}
}
