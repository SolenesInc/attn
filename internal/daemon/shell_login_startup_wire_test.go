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
	name, binary, startupFile, startup, histfile string
	userENV, zdotdir                             string
	bash                                         bool
}{
	{name: "bash", binary: "bash", startupFile: ".bash_profile", startup: posixStartupThatPrependsOnATerminal, histfile: ".bash_history", bash: true},
	{name: "zsh", binary: "zsh", startupFile: ".zshrc", startup: posixStartupThatPrependsOnATerminal},
	{name: "zsh with its own ZDOTDIR", binary: "zsh", startupFile: ".zshrc", startup: posixStartupThatPrependsOnATerminal, zdotdir: ".config/zsh"},
	{name: "sh", binary: "sh", startupFile: ".profile", startup: shStartupThatSetsItsOwnENV, userENV: ".shrc"},
	{name: "fish", binary: "fish", startupFile: ".config/fish/config.fish", startup: fishStartupThatPrependsOnATerminal},
}

func TestShellPanesKeepThePathTheirLoginStartupBuildsWithAttnFirst(t *testing.T) {
	for _, shell := range shellPaneStartups {
		t.Run(shell.name, func(t *testing.T) {
			binary, err := exec.LookPath(shell.binary)
			if err != nil {
				t.Skipf("%s is not installed", shell.binary)
			}
			if shell.bash && runtime.GOOS == "darwin" && binary == "/bin/bash" {
				t.Skip("this test wraps the shell to give it a home, and a wrapped macOS /bin/bash gets the bash 4 start")
			}
			w := &world{World: prepareWorld(t)}
			userHome := filepath.Join(w.Dir, "user-home")
			userBin := filepath.Join(userHome, "bin")
			userZdotdir := ""
			if shell.zdotdir != "" {
				userZdotdir = filepath.Join(userHome, shell.zdotdir)
				t.Setenv("ZDOTDIR", userZdotdir)
			}
			startupFile := filepath.Join(userHome, shell.zdotdir, shell.startupFile)
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
			envDump, shellDump := filepath.Join(w.Dir, "env.dump"), filepath.Join(w.Dir, "shell.dump")
			dump := "env > " + envDump
			if shell.histfile != "" {
				dump += `; { printf '%s\n' "$HISTFILE"; if [ -n "${BASH_VERSION-}" ] && shopt -q login_shell; then echo login; else echo not-login; fi; } > ` + shellDump
			}
			marker := "; echo dumped-$((1+1))"
			if shell.binary == "fish" {
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
			if exported["ZDOTDIR"] != userZdotdir {
				t.Errorf("the pane's commands see ZDOTDIR %q, want %q, the user's own", exported["ZDOTDIR"], userZdotdir)
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
			if shell.histfile != "" {
				raw, err := os.ReadFile(shellDump)
				if err != nil {
					t.Fatal(err)
				}
				histfile, login, _ := strings.Cut(strings.TrimSuffix(string(raw), "\n"), "\n")
				if want := filepath.Join(userHome, shell.histfile); histfile != want {
					t.Errorf("%s keeps history in %q, want %s", shell.name, histfile, want)
				}
				if shell.bash && login != "login" {
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
