package store

import (
	"path/filepath"
	"testing"
	"time"
)

func newFileActivityStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seedFileActivity(t *testing.T, s *Store, path, source, lastAt string, count int) {
	t.Helper()
	_, err := s.db.Exec(
		"INSERT INTO file_activity (path, source, last_at, count) VALUES (?, ?, ?, ?)",
		path, source, lastAt, count,
	)
	if err != nil {
		t.Fatalf("failed to seed file activity: %v", err)
	}
}

func TestRecordFileActivityAccumulatesCount(t *testing.T) {
	s := newFileActivityStore(t)

	s.RecordFileActivity("/docs/plan.md", FileActivitySourceOpened, "session-1")
	s.RecordFileActivity("/docs/plan.md", FileActivitySourceOpened, "session-2")

	files := s.GetRecentFiles(10, "")
	if len(files) != 1 {
		t.Fatalf("files = %d, want 1", len(files))
	}
	if files[0].Count != 2 {
		t.Errorf("count = %d, want 2", files[0].Count)
	}
	if files[0].SessionID == nil || *files[0].SessionID != "session-2" {
		t.Errorf("session_id = %v, want session-2", files[0].SessionID)
	}
}

func TestGetRecentFilesMergesSourcesIntoOneEntry(t *testing.T) {
	s := newFileActivityStore(t)

	now := time.Now()
	seedFileActivity(t, s, "/docs/plan.md", FileActivitySourceOpened, now.Add(-time.Hour).Format(time.RFC3339), 1)
	seedFileActivity(t, s, "/docs/plan.md", FileActivitySourceEdited, now.Format(time.RFC3339), 1)

	files := s.GetRecentFiles(10, "")
	if len(files) != 1 {
		t.Fatalf("files = %d, want one entry per file", len(files))
	}
	if files[0].Count != 2 {
		t.Errorf("count = %d, want both sources counted", files[0].Count)
	}
	if files[0].Source != FileActivitySourceEdited {
		t.Errorf("source = %q, want the most recent source", files[0].Source)
	}
}

func TestGetRecentFilesSurfacesAFileOnlyAnAgentTouched(t *testing.T) {
	s := newFileActivityStore(t)
	now := time.Now()

	seedFileActivity(t, s, "/docs/agent-wrote.md", FileActivitySourceEdited, now.Format(time.RFC3339), 1)
	seedFileActivity(t, s, "/docs/user-opened.md", FileActivitySourceOpened, now.Format(time.RFC3339), 1)

	files := s.GetRecentFiles(10, "")
	if len(files) != 2 {
		t.Fatalf("files = %d, want the edited file listed too", len(files))
	}
	if files[0].Path != "/docs/user-opened.md" || files[1].Path != "/docs/agent-wrote.md" {
		t.Errorf("order = %s, %s; want the opened file first", files[0].Path, files[1].Path)
	}
}

func TestGetRecentFilesPrefersTheCallersWorkspace(t *testing.T) {
	s := newFileActivityStore(t)
	now := time.Now()

	seedFileActivity(t, s, "/repo/docs/here.md", FileActivitySourceOpened, now.Format(time.RFC3339), 1)
	seedFileActivity(t, s, "/elsewhere/there.md", FileActivitySourceOpened, now.Format(time.RFC3339), 1)
	seedFileActivity(t, s, "/repo-other/near.md", FileActivitySourceOpened, now.Format(time.RFC3339), 1)

	files := s.GetRecentFiles(10, "/repo")
	if len(files) != 3 {
		t.Fatalf("files = %d, want every file still listed", len(files))
	}
	if files[0].Path != "/repo/docs/here.md" {
		t.Errorf("first = %s, want the in-workspace file", files[0].Path)
	}
}

func TestGetRecentFilesRanksByFrecency(t *testing.T) {
	s := newFileActivityStore(t)
	now := time.Now()
	frequentOld := "/docs/frequent-old.md"
	recentOnce := "/docs/recent-once.md"
	staleOnce := "/docs/stale-once.md"

	seedFileActivity(t, s, frequentOld, FileActivitySourceOpened, now.Add(-72*time.Hour).Format(time.RFC3339), 10)
	seedFileActivity(t, s, recentOnce, FileActivitySourceOpened, now.Format(time.RFC3339), 1)
	seedFileActivity(t, s, staleOnce, FileActivitySourceOpened, now.Add(-30*24*time.Hour).Format(time.RFC3339), 1)

	files := s.GetRecentFiles(10, "")
	want := []string{frequentOld, recentOnce, staleOnce}
	if len(files) != len(want) {
		t.Fatalf("files = %d, want %d", len(files), len(want))
	}
	for i, path := range want {
		if files[i].Path != path {
			t.Errorf("position %d = %s, want %s", i, files[i].Path, path)
		}
	}
}

func TestGetRecentFilesRanksBeforeTruncating(t *testing.T) {
	s := newFileActivityStore(t)
	now := time.Now()

	seedFileActivity(t, s, "/docs/frequent-old.md", FileActivitySourceOpened,
		now.Add(-30*24*time.Hour).Format(time.RFC3339), 100)
	for i := range 250 {
		seedFileActivity(t, s, filepath.Join("/docs", "fresh", string(rune('a'+i%26))+string(rune('a'+i/26))+".md"),
			FileActivitySourceOpened, now.Format(time.RFC3339), 1)
	}

	files := s.GetRecentFiles(5, "")
	if len(files) != 5 {
		t.Fatalf("files = %d, want 5", len(files))
	}
	if files[0].Path != "/docs/frequent-old.md" {
		t.Errorf("first = %s, want /docs/frequent-old.md", files[0].Path)
	}
}

func TestGetRecentFilesDoesNotStatMissingFiles(t *testing.T) {
	s := newFileActivityStore(t)

	s.RecordFileActivity("/nowhere/gone.md", FileActivitySourceOpened, "")
	if files := s.GetRecentFiles(10, ""); len(files) != 1 {
		t.Fatalf("files = %d, want the missing file still listed", len(files))
	}

	s.DeleteFileActivity("/nowhere/gone.md")
	if files := s.GetRecentFiles(10, ""); len(files) != 0 {
		t.Fatalf("files = %d, want the entry forgotten", len(files))
	}
}

func TestDeleteFileActivityForgetsEverySource(t *testing.T) {
	s := newFileActivityStore(t)
	s.RecordFileActivity("/docs/plan.md", FileActivitySourceOpened, "")
	s.RecordFileActivity("/docs/plan.md", FileActivitySourceEdited, "")
	s.RecordFileActivity("/docs/keep.md", FileActivitySourceOpened, "")

	s.DeleteFileActivity("/docs/plan.md")

	files := s.GetRecentFiles(10, "")
	if len(files) != 1 || files[0].Path != "/docs/keep.md" {
		t.Fatalf("files = %+v, want only /docs/keep.md", files)
	}
}
