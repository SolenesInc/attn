package daemon_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const posixStartupThatPrependsOnATerminal = `if [ -t 0 ]; then PATH="$HOME/bin:$PATH"; export PATH; fi
ATTN_TEST_STARTUP_HOME="$HOME"
export ATTN_TEST_STARTUP_HOME
`

const shStartupThatSetsItsOwnENV = posixStartupThatPrependsOnATerminal + `ENV="$HOME/.shrc"
export ENV
`

const fishStartupThatPrependsOnATerminal = `if isatty stdin; set -gx PATH $HOME/bin $PATH; end
set -gx ATTN_TEST_STARTUP_HOME $HOME
`

var shellPaneStartups = []struct {
	name, startupFile, startup, userENV string
	bash                                bool
}{
	{name: "bash", startupFile: ".bash_profile", startup: posixStartupThatPrependsOnATerminal, bash: true},
	{name: "sh", startupFile: ".profile", startup: shStartupThatSetsItsOwnENV, userENV: ".shrc"},
	{name: "fish", startupFile: ".config/fish/config.fish", startup: fishStartupThatPrependsOnATerminal},
}

func TestShellPanesKeepThePathTheirLoginStartupBuildsWithAttnFirst(t *testing.T) {
	for _, shell := range shellPaneStartups {
		t.Run(shell.name, func(t *testing.T) {
			binary, err := exec.LookPath(shell.name)
			if err != nil {
				t.Skipf("%s is not installed", shell.name)
			}
			if shell.bash && runtime.GOOS == "darwin" && binary == "/bin/bash" {
				t.Skip("this test wraps the shell to give it a home, and a wrapped macOS /bin/bash gets the bash 4 start")
			}
			w := &world{World: prepareWorld(t)}
			userHome := filepath.Join(w.Dir, "user-home")
			userBin := filepath.Join(userHome, "bin")
			startupFile := filepath.Join(userHome, shell.startupFile)
			for _, dir := range []string{userBin, filepath.Dir(startupFile)} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(startupFile, []byte(shell.startup), 0o644); err != nil {
				t.Fatal(err)
			}
			if shell.userENV != "" {
				if err := os.WriteFile(filepath.Join(userHome, shell.userENV), []byte("ATTN_TEST_USER_ENV_RAN=yes\nexport ATTN_TEST_USER_ENV_RAN\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("SHELL", shellWithAccountHome(t, w.Dir, binary, userHome))
			w.start()
			app := w.App()

			pane := w.Spawn(app, shellHarness, w.Path("shop"))
			envDump, bashDump := filepath.Join(w.Dir, "env.dump"), filepath.Join(w.Dir, "bash.dump")
			dump := "env > " + envDump
			if shell.bash {
				dump += `; { printf '%s\n' "$HISTFILE"; shopt -q login_shell && echo login || echo not-login; } > ` + bashDump
			}
			marker := "; echo dumped-$((1+1))"
			if shell.name == "fish" {
				marker = "; echo dumped-(math 1+1)"
			}
			app.TypeLine(pane, dump+marker)
			app.AwaitScreen(pane, "dumped-2")
			exported := readEnvDump(t, envDump)

			active := filepath.Join(w.Dir, "bin")
			path := filepath.SplitList(exported["PATH"])
			if len(path) < 2 || path[0] != active || path[1] != userBin || strings.Count(exported["PATH"], active) != 1 {
				t.Errorf("the pane's PATH is %q, want the active attn directory %s first and once, then the %s its login startup prepended", exported["PATH"], active, userBin)
			}
			if exported["HOME"] != userHome || exported["ATTN_TEST_STARTUP_HOME"] != userHome {
				t.Errorf("~/%s ran with HOME %q and the pane has HOME %q, want %s for both", shell.startupFile, exported["ATTN_TEST_STARTUP_HOME"], exported["HOME"], userHome)
			}
			for name, value := range exported {
				if strings.HasPrefix(name, "ATTN_SHELL_") || name == "ENV" && shell.userENV == "" || name == "HISTFILE" {
					t.Errorf("the pane's commands inherit %s=%q from attn's startup", name, value)
				}
			}
			if shell.userENV != "" {
				if want := filepath.Join(userHome, shell.userENV); exported["ENV"] != want || exported["ATTN_TEST_USER_ENV_RAN"] != "yes" {
					t.Errorf("the pane has ENV %q and its ENV file ran: %q, want the %s its startup set, run", exported["ENV"], exported["ATTN_TEST_USER_ENV_RAN"], want)
				}
			}
			if shell.bash {
				raw, err := os.ReadFile(bashDump)
				if err != nil {
					t.Fatal(err)
				}
				histfile, login, _ := strings.Cut(strings.TrimSuffix(string(raw), "\n"), "\n")
				if want := filepath.Join(userHome, ".bash_history"); histfile != want {
					t.Errorf("bash keeps history in %q, want %s", histfile, want)
				}
				if login != "login" {
					t.Errorf("the bash pane reports %s, want a login shell", login)
				}
			}
			exitShells(app, pane)
		})
	}
}

func readEnvDump(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	exported := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if name, value, ok := strings.Cut(line, "="); ok {
			exported[name] = value
		}
	}
	return exported
}

func shellWithAccountHome(t *testing.T, dir, binary, accountHome string) string {
	t.Helper()
	launcher := filepath.Join(dir, "account-shell", filepath.Base(binary))
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$HOME\" = " + shellQuoteForTest(os.Getenv("HOME")) + " ]; then\n  HOME=" + shellQuoteForTest(accountHome) + "\n  export HOME\nfi\nexec " + shellQuoteForTest(binary) + " \"$@\"\n"
	if err := os.WriteFile(launcher, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return launcher
}

func shellQuoteForTest(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
