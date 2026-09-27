package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type codexRollout struct {
	id, cwd, source   string
	started, modified time.Duration
}

func writeCodexRollout(t *testing.T, sessionsDir string, anchor time.Time, r codexRollout) string {
	t.Helper()
	dir := filepath.Join(sessionsDir, "2026", "05", "17")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	started := anchor.Add(r.started).UTC()
	path := filepath.Join(dir, fmt.Sprintf("rollout-%s-%s.jsonl", started.Format("2006-01-02T15-04-05"), r.id))
	line := fmt.Sprintf(`{"timestamp":"%[1]s","type":"session_meta","payload":{"id":"%[2]s","timestamp":"%[1]s","cwd":"%[3]s","source":"%[4]s"}}`+"\n",
		started.Format(time.RFC3339Nano), r.id, r.cwd, r.source)
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	modified := anchor.Add(r.modified)
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestACodexLaunchFindsTheInteractiveRolloutStartedInItsDirectory(t *testing.T) {
	anchor := time.Date(2026, 5, 17, 14, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		lookIn   string
		rollouts []codexRollout
		want     string
	}{
		{name: "a link to the directory finds the rollout recorded under its real path",
			lookIn:   "link/project",
			rollouts: []codexRollout{{id: "session", cwd: "real/project", source: "cli", started: time.Minute, modified: time.Minute}},
			want:     "session"},
		{name: "a classifier's exec rollout started later is not the session",
			lookIn: "real/project",
			rollouts: []codexRollout{
				{id: "session", cwd: "real/project", source: "cli", started: 5 * time.Second, modified: 5 * time.Second},
				{id: "classifier", cwd: "real/project", source: "exec", started: 11 * time.Second, modified: 11 * time.Second},
			},
			want: "session"},
		{name: "a resumed session found by modification time still skips exec rollouts",
			lookIn: "real/project",
			rollouts: []codexRollout{
				{id: "resumed", cwd: "real/project", source: "cli", started: -2 * time.Hour, modified: time.Minute},
				{id: "classifier", cwd: "real/project", source: "exec", started: -time.Hour, modified: 2 * time.Minute},
			},
			want: "resumed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "real", "project"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
				t.Fatal(err)
			}
			codexHome := filepath.Join(root, "codex-home")
			t.Setenv("CODEX_HOME", codexHome)
			paths := map[string]string{}
			for _, r := range tc.rollouts {
				r.cwd = filepath.Join(root, r.cwd)
				paths[r.id] = writeCodexRollout(t, filepath.Join(codexHome, "sessions"), anchor, r)
			}
			if got := FindCodexTranscript(filepath.Join(root, tc.lookIn), anchor); got != paths[tc.want] {
				t.Errorf("FindCodexTranscript = %q, want the %s rollout %q", got, tc.want, paths[tc.want])
			}
		})
	}
}

func TestAResumeLookupOpensOnlyRolloutsNamedForTheConversation(t *testing.T) {
	sessionsDir := t.TempDir()
	for i := range 512 {
		path := filepath.Join(sessionsDir, fmt.Sprintf("rollout-native-decoy-%03d.jsonl", i))
		if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"decoy"}}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := filepath.Join(sessionsDir, "rollout-2026-07-18-native-target.jsonl")
	if err := os.WriteFile(want, []byte(`{"type":"session_meta","payload":{"id":"native-target"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opened := 0
	got := findCodexTranscriptForResumeIn(sessionsDir, "native-target", func(path string) ([]byte, error) {
		opened++
		return readFirstJSONLLine(path)
	})
	if got != want || opened != 1 {
		t.Fatalf("the lookup found %q after opening %d rollouts, want %q after opening one", got, opened, want)
	}
}
