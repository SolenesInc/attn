package pty

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

const randomLaunchesPerShell = 8

var hostileArguments = []string{"", " ", "a b", "'", `"`, `\`, "$HOME", "`id`", "-n", "--", "-c", "a\nb", "*", "~", "é\xff"}

func TestLoginShellPreservesAgentArgumentsAndEnvironment(t *testing.T) {
	for _, shell := range []string{"sh", "bash", "zsh", "fish", "dash"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s is unavailable: %v", shell, err)
			}
			dir := filepath.Join(t.TempDir(), "agent=command")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			probe := filepath.Join(dir, "sh")
			if err := os.Symlink("/bin/sh", probe); err != nil {
				t.Fatal(err)
			}
			check := func(t *testing.T, args []string, value string) {
				argv := append([]string{probe, "-c", `printf '%s\000' "$ATTN_ARGV_PROBE" "$@"`, "probe"}, args...)
				cmd := loginShellCommand(path, argv, []string{"ATTN_ARGV_PROBE=" + value})
				cmd.Env = os.Environ()
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("launch failed: %v: %s", err, stderr.String())
				}
				got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
				want := append([]string{value}, args...)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("received %q, want %q", got, want)
				}
			}
			check(t, hostileArguments, "it's \"$HOME\" `x`\n-n")
			text := rapid.String().Filter(func(s string) bool { return !strings.ContainsRune(s, 0) })
			for i := 0; i < randomLaunchesPerShell; i++ {
				check(t, rapid.SliceOf(text).Example(i), text.Example(i+randomLaunchesPerShell))
			}
		})
	}
}
