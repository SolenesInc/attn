package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAReloadedPluginAgentKeepsTheEnvironmentTheDaemonCaptured(t *testing.T) {
	w := &world{World: prepareWorld(t)}
	broken := filepath.Join(w.Dir, "login-shell-broken")
	shell := filepath.Join(w.Dir, "login-shell")
	script := "#!/bin/sh\nif [ \"$3\" = 'env -0' ]; then\n  [ -e '" + broken + "' ] && exit 23\n  export ATTN_TEST_CAPTURED=present\nfi\nexec /bin/sh \"$@\"\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	report := filepath.Join(t.TempDir(), "report")
	if err := syscall.Mkfifo(report, 0o600); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(w.Dir, "reloaded-agent")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\nprintf '%s' \"$ATTN_TEST_CAPTURED\" > '"+report+"'\nexec cat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.start()
	app := w.App()
	driver := connectDriver(t, w, "attn-plug", "plug", map[string]bool{"state_reporting": true, "resume": true})
	awaitDriverAvailable(app, "plug")
	session, _ := spawnDriven(w, app, driver, w.Path("shop"))

	if err := os.WriteFile(broken, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	driver.mu.Lock()
	driver.holdLaunch = true
	driver.mu.Unlock()
	app.Send(protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 80, Rows: 24})
	var reloading driverLaunch
	reply := driver.asked("driver.resume", &reloading)
	driver.launched()
	driver.answer(reply, map[string]any{"argv": []string{agent}})

	got, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "present" {
		t.Fatalf("the reloaded agent has ATTN_TEST_CAPTURED=%q, want the value the daemon captured at start", got)
	}
}

func TestAPluginStartsWithTheLoginEnvironment(t *testing.T) {
	w := &world{World: prepareWorld(t)}
	shell := filepath.Join(w.Dir, "login-shell")
	script := "#!/bin/sh\nexport PATH=/login/bin:$PATH ATTN_TEST_LOGIN_ONLY=present\nexec /bin/sh \"$@\"\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	report := filepath.Join(t.TempDir(), "report")
	if err := syscall.Mkfifo(report, 0o600); err != nil {
		t.Fatal(err)
	}
	checkout := pluginsCheckout(t, w.Path("checkouts", "probe"), "probe", "0.1.0",
		"#!/bin/sh\nprintf '%s %s' \"$ATTN_TEST_LOGIN_ONLY\" \"$PATH\" > '"+report+"'\nexec sleep 3600\n")
	w.start()

	pluginsAct(t, w.App(), protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: checkout}, "install", "probe")

	got, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	loginOnly, path, _ := strings.Cut(string(got), " ")
	if loginOnly != "present" || !slices.Contains(filepath.SplitList(path), "/login/bin") {
		t.Fatalf("the plugin started with %q, want the login environment: ATTN_TEST_LOGIN_ONLY=present and /login/bin on PATH", got)
	}
}
