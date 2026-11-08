package main_test

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/testworld"
)

func printedLines(stdout string) []string {
	if stdout == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
}

func TestDebugReadersPrintTheLogsTheyAreAskedFor(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	logPath := filepath.Join(s.Dir, "daemon.log")

	missing := s.Attn("debug", "daemon-log")
	if missing.Code != 1 || !strings.Contains(missing.Stderr, "no such file: "+logPath) {
		t.Errorf("debug daemon-log over a missing log exited %d: %s", missing.Code, missing.Stderr)
	}

	stamp := func(ago time.Duration, text string) string {
		return "[" + time.Now().Add(-ago).Format("2006-01-02 15:04:05") + "] INFO: " + text
	}
	orphan := "  continuation of an entry rotated away"
	old := stamp(2*time.Hour, "old entry")
	oldTail := "  continuation of the old entry"
	recent := stamp(30*time.Minute, "recent entry")
	recentTail := "  continuation of the recent entry"
	long := stamp(time.Minute, strings.Repeat("x", 200*1024))
	last := stamp(time.Minute, "last entry without a newline")
	all := []string{orphan, old, oldTail, recent, recentTail, long, last}
	if err := os.WriteFile(logPath, []byte(strings.Join(all, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"everything by default", nil, all},
		{"everything with no tail limit", []string{"--tail", "0"}, all},
		{"everything with a negative tail", []string{"--tail", "-1"}, all},
		{"the last lines", []string{"--tail", "2"}, []string{long, last}},
		{"entries since a cutoff with their continuations", []string{"--since", "1h"}, []string{recent, recentTail, long, last}},
		{"no continuation without an entry before it", []string{"--since", "3h"}, all[1:]},
		{"lines matching a regexp", []string{"--grep", "rec.nt"}, []string{recent, recentTail}},
		{"filters combined", []string{"--since", "1h", "--grep", "entry", "--tail", "1"}, []string{last}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := s.Attn(append([]string{"debug", "daemon-log"}, tc.args...)...)
			if r.Code != 0 {
				t.Fatalf("exited %d: %s", r.Code, r.Stderr)
			}
			if got := printedLines(r.Stdout); !slices.Equal(got, tc.want) {
				t.Errorf("printed %d lines, want %d:\n%.400s", len(got), len(tc.want), r.Stdout)
			}
		})
	}

	for _, tc := range []struct {
		name   string
		args   []string
		stderr string
	}{
		{"an invalid regexp", []string{"--grep", "("}, "invalid --grep pattern"},
		{"an invalid duration", []string{"--since", "soon"}, `invalid --since duration "soon"`},
		{"a stray argument", []string{"everything"}, "unexpected arguments"},
	} {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			r := s.Attn(append([]string{"debug", "daemon-log"}, tc.args...)...)
			if r.Code != 2 || !strings.Contains(r.Stderr, tc.stderr) || r.Stdout != "" {
				t.Errorf("exited %d with stdout %q and stderr %q, want 2 and %q", r.Code, r.Stdout, r.Stderr, tc.stderr)
			}
		})
	}

	t.Run("input exports only complete input records", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("the frontend debug directory sits under the real home outside Linux, where XDG_DATA_HOME does not move it")
		}
		dataHome := filepath.Join(s.Dir, "xdg")
		env := []string{"XDG_DATA_HOME=" + dataHome}

		missing := s.Run(testworld.Invocation{Args: []string{"debug", "input"}, Env: env})
		_, diagnostics, found := strings.Cut(strings.TrimSpace(missing.Stderr), "no such file: ")
		if missing.Code != 1 || !found || !strings.HasPrefix(diagnostics, dataHome+string(filepath.Separator)) || filepath.Base(diagnostics) != "terminal-diagnostics.jsonl" {
			t.Fatalf("debug input over a missing log exited %d: %s", missing.Code, missing.Stderr)
		}

		input := `{"kind":"input","runtimeId":"one","counts":{"keydown:composing":2}}`
		records := []string{
			`{"kind":"paint","context":{"kind":"input"},"text":"private terminal output"}`,
			input,
			`{"kind":"input","truncated":`,
			`not json`,
		}
		if err := os.MkdirAll(filepath.Dir(diagnostics), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(diagnostics, []byte(strings.Join(records, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		r := s.Run(testworld.Invocation{Args: []string{"debug", "input", "--tail", "0"}, Env: env})
		if got := printedLines(r.Stdout); r.Code != 0 || !slices.Equal(got, []string{input}) {
			t.Errorf("debug input exited %d and exported %q, want only the complete input record", r.Code, got)
		}
	})
}
