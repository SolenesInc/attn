package daemon_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellPanesKeepThePathTheirLoginStartupBuildsWithAttnFirst(t *testing.T) {
	for _, shell := range []struct {
		name, startupFile, histfile string
	}{
		{name: "bash", startupFile: ".bash_profile", histfile: ".bash_history"},
		{name: "zsh", startupFile: ".zshrc"},
		{name: "sh", startupFile: ".profile"},
	} {
		t.Run(shell.name, func(t *testing.T) {
			binary, err := exec.LookPath(shell.name)
			if err != nil {
				t.Skipf("%s is not installed", shell.name)
			}
			w := &world{World: prepareWorld(t)}
			userHome := filepath.Join(w.Dir, "user-home")
			userBin := filepath.Join(userHome, "bin")
			if err := os.MkdirAll(userBin, 0o755); err != nil {
				t.Fatal(err)
			}
			startup := `if [ -t 0 ]; then PATH="$HOME/bin:$PATH"; fi
attn_startup_home="$HOME"
`
			if err := os.WriteFile(filepath.Join(userHome, shell.startupFile), []byte(startup), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SHELL", shellWithAccountHome(t, w.Dir, binary, shell.name, userHome))
			w.start()
			app := w.App()

			pane := w.Spawn(app, shellHarness, w.Path("shop"))
			dump := filepath.Join(w.Dir, "startup.dump")
			app.TypeLine(pane, `printf '%s\n' "$PATH" "$HISTFILE" "$attn_startup_home" > `+dump+`; echo dumped-$((1+1))`)
			app.AwaitScreen(pane, "dumped-2")
			raw, err := os.ReadFile(dump)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
			if len(lines) != 3 {
				t.Fatalf("the pane dumped %q, want PATH, HISTFILE and the startup file's HOME", raw)
			}
			path, histfile, startupHome := filepath.SplitList(lines[0]), lines[1], lines[2]

			active := filepath.Join(w.Dir, "bin")
			if len(path) < 2 || path[0] != active || path[1] != userBin || strings.Count(lines[0], active) != 1 {
				t.Errorf("the pane's PATH is %q, want the active attn directory %s first and once, then the %s its login startup prepended", lines[0], active, userBin)
			}
			if startupHome != userHome {
				t.Errorf("~/%s ran with HOME %q, want %s", shell.startupFile, startupHome, userHome)
			}
			if shell.histfile != "" && histfile != filepath.Join(userHome, shell.histfile) {
				t.Errorf("%s started with HISTFILE %q, want it under the user's home %s", shell.name, histfile, userHome)
			}
			exitShells(app, pane)
		})
	}
}

func shellWithAccountHome(t *testing.T, dir, binary, name, accountHome string) string {
	t.Helper()
	launcher := filepath.Join(dir, "account-shell", name)
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
