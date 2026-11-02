package pty

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShellIntegrationIsExpectedOnlyFromShellsAttnTeachesCommandMarks(t *testing.T) {
	dir := t.TempDir()
	bash := filepath.Join(dir, "bin", "bash")
	if err := os.MkdirAll(filepath.Dir(bash), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bash, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	link := func(name, target string) string {
		path := filepath.Join(dir, name)
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, tc := range []struct {
		name  string
		shell string
		env   []string
		want  bool
	}{
		{"bash by its own name", bash, nil, true},
		{"a renamed link to bash", link("bash5", bash), nil, true},
		{"sh linked to bash starts in POSIX mode, without marks", link("sh", bash), nil, false},
		{"zsh", filepath.Join(dir, "zsh"), nil, true},
		{"fish", filepath.Join(dir, "fish"), nil, true},
		{"an unknown shell", filepath.Join(dir, "nu"), nil, false},
		{"marks turned off", bash, []string{"ATTN_NO_SHELL_INTEGRATION=1"}, false},
	} {
		if got := ShellIntegrationExpected(tc.shell, tc.env); got != tc.want {
			t.Errorf("%s: ShellIntegrationExpected(%s) = %v, want %v", tc.name, tc.shell, got, tc.want)
		}
	}
}
