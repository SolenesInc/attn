package store

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestFileActivityCountsALateTouchAtItsOwnTimeAndKeepsLastAt(t *testing.T) {
	s := New()
	day := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	earlier := day.Add(-48 * time.Hour)
	for _, touch := range []struct {
		path string
		at   time.Time
	}{
		{"/alone.md", day},
		{"/late.md", day}, {"/late.md", earlier},
		{"/in-order.md", earlier}, {"/in-order.md", day},
		{"/twice.md", day}, {"/twice.md", day},
	} {
		s.RecordFileActivity(FileActivity{Path: touch.path, Source: FileActivitySourceOpened, SessionID: "s1", At: touch.at})
	}
	files := s.GetRecentFiles()
	var ranked []string
	for _, file := range files {
		ranked = append(ranked, file.Path)
		if file.Path == "/late.md" && (file.Count != 2 || file.LastAt != day.Local().Format(time.RFC3339)) {
			t.Fatalf("late.md = %+v, want two touches with the latest one's time %s", file, day.Local().Format(time.RFC3339))
		}
	}
	if want := []string{"/twice.md", "/in-order.md", "/late.md", "/alone.md"}; !slices.Equal(ranked, want) {
		t.Fatalf("ranked = %v, want %v: a touch reported late counts as much as the same touch reported in order, and less than a touch now", ranked, want)
	}
}

func TestFileActivityUseScoreMigrationStartsAtEachRowsCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.db")
	db, err := OpenDBAtSchemaVersion(path, 1795458231132986)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO file_activity(host,path,source,session_id,last_at,count) VALUES('','/work/plan.md','opened','s1','2026-11-09T10:00:00+01:00',4)`,
		`INSERT INTO file_activity(host,path,source,session_id,last_at,count) VALUES('','/work/plan.md','edited',NULL,'2026-11-09T11:00:00+01:00',2)`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	rows, err := upgraded.Query(`SELECT source, count, use_score FROM file_activity ORDER BY source`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct {
		source string
		count  int
		use    float64
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.source, &r.count, &r.use); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if want := []row{{"edited", 2, 2}, {"opened", 4, 4}}; !slices.Equal(got, want) {
		t.Fatalf("migrated file activity = %+v, want use_score started at each row's count %+v", got, want)
	}
}
