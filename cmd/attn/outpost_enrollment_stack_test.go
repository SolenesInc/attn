package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestLeavingAnOutpostRoutesSocketCommandsLocally(t *testing.T) {
	for _, duringStartup := range []bool{false, true} {
		name := "running"
		if duringStartup {
			name = "during startup"
		}
		t.Run(name, func(t *testing.T) {
			home, outpost := testworld.NewStack(t), testworld.NewStack(t)
			pair := testworld.PrepareOutpostPair(t, home, outpost)
			pair.Start()
			requireStdout(t, home.Attn("seed", "plant", "Home garden before leave", "-m", "Written at the home"))
			requireStdout(t, outpost.Attn("seed", "ls"), "Home garden before leave")

			mark := pair.App.EventCount()
			requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"), "false")
			testworld.AwaitSince(pair.App, mark, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
				return len(m.Outposts) == 1 && m.Outposts[0].State == protocol.OutpostStateLost
			})
			var loaded *testworld.Pause
			var ready func()
			if duringStartup {
				outpost.Stop()
				loaded = outpost.PauseAt(pausepoint.DaemonEnrollmentLoaded)
				ready = outpost.StartHeldBeforeReady(loaded)
			}
			requireStdout(t, outpost.Attn("enrollment", "leave"), "no longer enrolled")
			if duringStartup {
				loaded.Release()
				ready()
			}
			requireStdout(t, outpost.Attn("seed", "plant", "Local garden after leave", "-m", "Written on the former outpost"))
			requireStdout(t, outpost.Attn("seed", "ls"), "Local garden after leave")
			if result := home.Attn("seed", "ls"); result.Code != 0 || strings.Contains(result.Stdout, "Local garden after leave") {
				t.Fatalf("leaving changed the home's garden: %+v", result)
			}
			if !duringStartup {
				outpost.Stop()
				outpost.Start()
				requireStdout(t, outpost.Attn("seed", "ls"), "Local garden after leave")
			}
			t.Log("leave switches socket commands to the local garden")
		})
	}
}
