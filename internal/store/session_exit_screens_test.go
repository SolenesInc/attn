package store

import (
	"testing"
	"time"
)

func TestSessionExitScreenRoundTripsAndReplaces(t *testing.T) {
	s := newSessionOwnedTableStore(t)
	addSessionInDirectory(t, s, "s1", "/tmp/one")

	if got := s.GetSessionExitScreen("s1"); got != nil {
		t.Fatalf("exit screen before any exit = %+v, want none", got)
	}
	first := SessionExitScreen{SessionID: "s1", Text: "Error: boom\n", Cols: 80, Rows: 24, ExitCode: 1}
	if err := s.SaveSessionExitScreen(first, time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	got := s.GetSessionExitScreen("s1")
	if got == nil || got.Text != first.Text || got.Cols != 80 || got.Rows != 24 || got.ExitCode != 1 || got.ExitedAt != "2026-09-01T10:00:00Z" {
		t.Fatalf("exit screen = %+v", got)
	}

	second := SessionExitScreen{SessionID: "s1", Text: "killed\n", Cols: 100, Rows: 30, ExitCode: 0, ExitSignal: "SIGTERM"}
	if err := s.SaveSessionExitScreen(second, time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	got = s.GetSessionExitScreen("s1")
	if got == nil || got.Text != "killed\n" || got.ExitSignal != "SIGTERM" || got.ExitedAt != "2026-09-01T11:00:00Z" {
		t.Fatalf("exit screen after a second exit = %+v, want the newer one", got)
	}

	if err := s.DeleteSessionExitScreen("s1"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetSessionExitScreen("s1"); got != nil {
		t.Fatalf("exit screen after delete = %+v, want none", got)
	}
}
