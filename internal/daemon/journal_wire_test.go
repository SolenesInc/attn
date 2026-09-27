package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJournalAppendKeepsEveryEntryInItsDaysFileAndRefusesMalformedOnes(t *testing.T) {
	w := newWorld(t)
	notebook := filepath.Join(w.Dir, "notebook")
	if err := os.MkdirAll(notebook, 0o700); err != nil {
		t.Fatal(err)
	}
	cli := w.Client()

	var hashes []string
	for _, entry := range []string{"shipped the parser", "reviewed the store"} {
		appended, err := cli.AppendJournal("", "2026-07-05", entry)
		if err != nil {
			t.Fatalf("append %q: %v", entry, err)
		}
		if appended.RelPath != "journal/2026-07-05.md" || appended.Hash == "" {
			t.Fatalf("append %q answered %+v, want journal/2026-07-05.md and its hash", entry, appended)
		}
		hashes = append(hashes, appended.Hash)
	}
	if hashes[0] == hashes[1] {
		t.Errorf("both appends answered hash %s, want the file's hash after each", hashes[0])
	}
	journal, err := os.ReadFile(filepath.Join(notebook, "journal", "2026-07-05.md"))
	if err != nil {
		t.Fatal(err)
	}
	shipped, reviewed := strings.Index(string(journal), "shipped the parser"), strings.Index(string(journal), "reviewed the store")
	if shipped < 0 || reviewed < shipped {
		t.Errorf("journal/2026-07-05.md holds:\n%s\nwant the first entry, then the second", journal)
	}

	before := time.Now().Format("2006-01-02")
	today, err := cli.AppendJournal("", "", "an undated entry")
	after := time.Now().Format("2006-01-02")
	if err != nil {
		t.Fatalf("append without a date: %v", err)
	}
	if today.RelPath != "journal/"+before+".md" && today.RelPath != "journal/"+after+".md" {
		t.Errorf("an undated entry landed in %s, want today's journal/%s.md", today.RelPath, after)
	}

	for _, refused := range []struct{ date, entry, want string }{
		{"2026-07-05", "   ", "entry is required"},
		{"not-a-date", "an entry", "not-a-date"},
	} {
		if _, err := cli.AppendJournal("", refused.date, refused.entry); err == nil || !strings.Contains(err.Error(), refused.want) {
			t.Errorf("append %q on %q answered %v, want a refusal naming %q", refused.entry, refused.date, err, refused.want)
		}
	}
	unchanged, err := os.ReadFile(filepath.Join(notebook, "journal", "2026-07-05.md"))
	if err != nil || string(unchanged) != string(journal) {
		t.Errorf("a refused append changed journal/2026-07-05.md to:\n%s", unchanged)
	}
}
