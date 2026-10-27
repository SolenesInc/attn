package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestDirectLaunchReadsTheCurrentProfileEvenWhenEnvironmentCaptureFails(t *testing.T) {
	for _, failedCapture := range []bool{false, true} {
		t.Run(strconv.FormatBool(failedCapture), func(t *testing.T) {
			w := &world{World: prepareWorld(t, fakeagent.Claude, fakeagent.Codex)}
			profile := filepath.Join(w.Dir, "profile")
			shell := filepath.Join(w.Dir, "login-shell")
			script := "#!/bin/sh\n. \"$ATTN_TEST_PROFILE\"\n"
			if failedCapture {
				script += "if [ \"$3\" = 'env -0' ]; then exit 23; fi\n"
			}
			script += "exec /bin/sh \"$@\"\n"
			if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SHELL", shell)
			t.Setenv("ATTN_TEST_PROFILE", profile)
			if err := os.WriteFile(profile, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			w.start()
			app := w.App()
			setSetting(t, app, "direct_agent_launch_enabled", "true")
			for _, phase := range []string{"first", "edited", "removed"} {
				var content strings.Builder
				for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
					key := "ATTN_" + strings.ToUpper(string(h)) + "_EXECUTABLE"
					if phase == "removed" {
						fmt.Fprintf(&content, "unset %s\n", key)
						continue
					}
					executable := filepath.Join(w.Dir, string(h)+"="+phase)
					native := filepath.Join(w.Dir, "bin", string(h))
					shim := fmt.Sprintf("#!/bin/sh\nexport ATTN_TEST_EXECUTABLE=%s\nexec '%s' \"$@\"\n", phase, native)
					if err := os.WriteFile(executable, []byte(shim), 0o755); err != nil {
						t.Fatal(err)
					}
					fmt.Fprintf(&content, "export %s='%s'\n", key, executable)
				}
				if err := os.WriteFile(profile, []byte(content.String()), 0o600); err != nil {
					t.Fatal(err)
				}
				for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
					run := w.Launched(w.Spawn(app, h, w.Path(string(h), phase), func(m *protocol.SpawnSessionMessage) { m.InitialPrompt = protocol.Ptr(literalInitialPrompt) }))
					if got := run.Prompted(); got != literalInitialPrompt {
						t.Fatalf("%s prompt %q", h, got)
					}
					marker, _ := envEntry(run.Env, "ATTN_TEST_EXECUTABLE")
					want := phase
					if phase == "removed" {
						want = ""
					}
					if marker != want {
						t.Fatalf("%s ran executable %q after profile %s; want %q", h, marker, phase, want)
					}
				}
			}
			if err := os.WriteFile(profile, []byte("export ATTN_CODEX_EXECUTABLE=/no/such/profile-codex\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			run := w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("explicit"), func(m *protocol.SpawnSessionMessage) { m.Executable = protocol.Ptr("codex") }))
			if value, _ := envEntry(run.Env, "ATTN_CODEX_EXECUTABLE"); value != "codex" {
				t.Fatalf("explicit default executable left hook override %q", value)
			}
		})
	}
}
