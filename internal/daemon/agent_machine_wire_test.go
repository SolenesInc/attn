package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAgentLaunchUsesTheMachineExecutableSettingAndReloadReadsItAgain(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(strconv.FormatBool(direct), func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app := w.App()
			setSetting(t, app, "direct_agent_launch_enabled", strconv.FormatBool(direct))
			executable := func(name string) string {
				path := filepath.Join(w.Dir, name)
				body := fmt.Sprintf("#!/bin/sh\nexport LAUNCHED_BY=%s\nexec %s/bin/claude \"$@\"\n", name, w.Dir)
				if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
				return path
			}
			first, second := executable("first"), executable("second")
			setSetting(t, app, "claude_executable", first)
			session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
			assertLauncher := func(want string) {
				t.Helper()
				run := w.Launched(session)
				if !slices.Contains(run.Env, "LAUNCHED_BY="+want) {
					t.Errorf("agent did not use machine executable %s", want)
				}
			}
			assertLauncher("first")
			setSetting(t, app, "claude_executable", second)
			launchIntentReload(t, app, session)
			assertLauncher("second")
		})
	}
}

func TestAnUnavailableHarnessIsRefusedNamingTheMachine(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	if err := os.Remove(filepath.Join(w.Dir, "bin", "claude")); err != nil {
		t.Fatal(err)
	}
	result, _, _ := w.RequestSpawn(app, fakeagent.Claude, w.Path("shop"))
	machine, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || !strings.Contains(protocol.Deref(result.Error), machine) || !strings.Contains(protocol.Deref(result.Error), "claude") {
		t.Fatalf("missing harness = %+v, want a refusal naming claude and %s", result, machine)
	}
}

func TestAgentHooksReachTheLaunchingDaemonAndDefaultExecutableComesFromPath(t *testing.T) {
	for _, direct := range []bool{false, true} {
		for _, agent := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
			t.Run(string(agent)+"/direct="+strconv.FormatBool(direct), func(t *testing.T) {
				w := &world{World: prepareWorld(t, agent)}
				t.Setenv("ATTN_CLAUDE_EXECUTABLE", "")
				t.Setenv("ATTN_CODEX_EXECUTABLE", "")
				w.start()
				app := w.App()
				setSetting(t, app, "direct_agent_launch_enabled", strconv.FormatBool(direct))
				setSetting(t, app, "codex_shared_enabled", "false")
				session := w.Spawn(app, agent, w.Path("shop"))
				run := w.Launched(session)
				if run.Argv[0] != string(agent) {
					t.Errorf("executable = %q, want the harness on this daemon's PATH", run.Argv[0])
				}
				if !strings.Contains(strings.Join(run.Argv, "\n"), w.Socket) {
					t.Errorf("hooks %q do not name the launching daemon's socket %s", run.Argv, w.Socket)
				}
				app.TypeLine(session, "price the cart")
				run.Prompted()
				working := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
				run.Reply("Priced. <!-- attn:state=idle -->")
				testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
			})
		}
	}
}
