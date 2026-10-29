package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestHarnessesOnTheLoginPathServeLaunchAndHeadlessWork(t *testing.T) {
	for _, harness := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		t.Run(string(harness), func(t *testing.T) {
			w := &world{World: prepareWorld(t, harness)}
			bin := filepath.Join(w.Dir, "bin")
			localBin := filepath.Join(w.Dir, ".local", "bin")
			if err := os.MkdirAll(localBin, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"claude", "codex", "fakeagent.json"} {
				if err := os.Rename(filepath.Join(bin, name), filepath.Join(localBin, name)); err != nil {
					t.Fatal(err)
				}
			}
			shell := filepath.Join(w.Dir, "login-shell")
			script := fmt.Sprintf("#!/bin/sh\nexport PATH='%s:%s:/usr/bin:/bin'\nshift\nexec /bin/sh \"$@\"\n", localBin, bin)
			if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SHELL", shell)
			t.Setenv("PATH", bin+":/usr/bin:/bin")
			t.Setenv("ATTN_CLAUDE_EXECUTABLE", "")
			t.Setenv("ATTN_CODEX_EXECUTABLE", "")
			t.Setenv("ATTN_HEADLESS_TASKS", "on")
			w.start()
			app := w.App()
			name := string(harness)
			if available := app.Initial.Settings[name+"_available"]; available != "true" {
				t.Fatalf("%s is available only in the login shell, got available=%q", name, available)
			}
			models := harnessCatalog(app, name, false)
			if !models.Success || len(models.Models) == 0 {
				t.Fatalf("%s model discovery = %+v", name, models)
			}
			setSetting(t, app, name+"_executable", name)
			setSetting(t, app, name+"_executable", "")
			session := w.Spawn(app, harness, w.Path("shop"))
			agent := w.Launched(session)
			app.TypeLine(session, "deploy to staging")
			agent.Prompted()
			w.HeadlessTask().Answer("Staging deploy")
			awaitLabel(app, session, "Staging deploy")
			agent.Reply("Staging is ready. Should I deploy to production too?")
			w.HeadlessTask().Answer(`{"verdict":"WAITING"}`)
			testworld.AwaitSession(app, session, func(s protocol.Session) bool {
				return s.State == protocol.SessionStateWaitingInput
			})
		})
	}
}

func TestMachineExecutableSettingWinsOverHarnessEnvironmentForHeadlessWork(t *testing.T) {
	for _, harness := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		t.Run(string(harness), func(t *testing.T) {
			w := newTitlingWorld(t, harness)
			app := w.App()
			name := string(harness)
			setSetting(t, app, name+"_executable", filepath.Join(w.Dir, "bin", name))
			t.Setenv("ATTN_"+strings.ToUpper(name)+"_EXECUTABLE", filepath.Join(w.Dir, "missing-harness"))
			models := harnessCatalog(app, name, true)
			if !models.Success || len(models.Models) == 0 {
				t.Fatalf("%s ignored its machine executable setting: %+v", name, models)
			}
			session := w.Spawn(app, harness, w.Path("shop"))
			agent := w.Launched(session)
			app.TypeLine(session, "deploy to staging")
			agent.Prompted()
			w.HeadlessTask().Answer("Staging deploy")
			awaitLabel(app, session, "Staging deploy")
			agent.Reply("Staging is ready. Should I deploy to production too?")
			w.HeadlessTask().Answer(`{"verdict":"WAITING"}`)
			testworld.AwaitSession(app, session, func(s protocol.Session) bool {
				return s.State == protocol.SessionStateWaitingInput
			})
		})
	}
}
