package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/testworld"
)

func writeRestoreFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readRestoreFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertNoRestoreFile(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s should not exist (stat: %v)", path, err)
		}
	}
}

func restoredLines(t *testing.T, r testworld.Result) (from, preserved string) {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("attn db restore exited %d: %s", r.Code, r.Stderr)
	}
	for _, line := range strings.Split(r.Stdout, "\n") {
		if rest, ok := strings.CutPrefix(line, "restored attn.db from: "); ok {
			from = rest
		}
		if rest, ok := strings.CutPrefix(line, "previous attn.db preserved as: "); ok {
			preserved = rest
		}
	}
	if from == "" {
		t.Fatalf("attn db restore did not say what it restored from:\n%s", r.Stdout)
	}
	return from, preserved
}

func TestDBRestoreSwapsInTheNewestBackupAndKeepsEveryDatabaseItReplaces(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	db := filepath.Join(s.Dir, "attn.db")
	backups := filepath.Join(s.Dir, "backups")

	noBackups := s.Attn("db", "restore")
	if noBackups.Code != 1 || !strings.Contains(noBackups.Stderr, backups) {
		t.Errorf("a restore with no backups directory exited %d: %s", noBackups.Code, noBackups.Stderr)
	}
	assertNoRestoreFile(t, db)

	s.Start()
	running := s.Attn("db", "restore")
	if running.Code != 1 || !strings.Contains(running.Stderr, "stop it first") {
		t.Errorf("a restore under a running daemon exited %d: %s", running.Code, running.Stderr)
	}
	s.Stop()
	live := readRestoreFile(t, db)

	for _, refused := range []struct {
		name, source, stderr string
	}{
		{"the live database", db, "live database"},
		{"a directory", s.Dir, "not a regular file"},
	} {
		t.Run("refuses "+refused.name, func(t *testing.T) {
			r := s.Attn("db", "restore", refused.source)
			if r.Code != 1 || !strings.Contains(r.Stderr, refused.stderr) {
				t.Errorf("exited %d with %q, want 1 and %q", r.Code, r.Stderr, refused.stderr)
			}
			if got := readRestoreFile(t, db); got != live {
				t.Error("a refused restore changed attn.db")
			}
			if preserved, _ := filepath.Glob(db + ".pre-restore-*"); len(preserved) != 0 {
				t.Errorf("a refused restore preserved %v", preserved)
			}
		})
	}

	newest := filepath.Join(backups, "attn-20990601-000000.db")
	older := filepath.Join(backups, "attn-20990101-000000.db")
	writeRestoreFile(t, older, "older snapshot")
	writeRestoreFile(t, newest, "newest snapshot")
	writeRestoreFile(t, filepath.Join(backups, "attn-premigration-99-21000101-000000.db"), "premigration snapshot")
	writeRestoreFile(t, db+"-wal", "live wal")
	writeRestoreFile(t, db+"-shm", "live shm")
	now := time.Now().UTC()
	strays := map[string]string{}
	for at := now; !at.After(now.Add(fakeagent.HangGuard)); at = at.Add(time.Second) {
		stamp := at.Format("20060102-150405")
		strays[stamp] = db + ".pre-restore-" + stamp + "-wal"
		writeRestoreFile(t, strays[stamp], "unrelated file")
	}
	assertCollided := func(preserved string) {
		t.Helper()
		for stamp := range strays {
			if n, ok := strings.CutPrefix(preserved, db+".pre-restore-"+stamp+"-"); ok && n != "" && strings.Trim(n, "0123456789") == "" {
				return
			}
		}
		t.Errorf("preserved as %s, want a numbered name beside the stray for its second", preserved)
	}

	from, first := restoredLines(t, s.Attn("db", "restore"))
	if from != newest {
		t.Errorf("restored from %s, want the newest rotating backup %s", from, newest)
	}
	if first == "" {
		t.Fatal("the live database was not preserved")
	}
	assertCollided(first)
	for path, want := range map[string]string{
		db:             "newest snapshot",
		newest:         "newest snapshot",
		first:          live,
		first + "-wal": "live wal",
		first + "-shm": "live shm",
	} {
		if got := readRestoreFile(t, path); got != want {
			t.Errorf("%s holds %q, want %q", path, got, want)
		}
	}
	assertNoRestoreFile(t, db+"-wal", db+"-shm")

	again, second := restoredLines(t, s.Attn("db", "restore", "latest"))
	if again != newest || second == first {
		t.Errorf("restore latest came from %s and preserved as %s, want %s and a name other than %s", again, second, newest, first)
	}
	assertCollided(second)
	if readRestoreFile(t, first) != live || readRestoreFile(t, second) != "newest snapshot" {
		t.Error("a second restore overwrote the first preserved database")
	}
	for _, stray := range strays {
		if got := readRestoreFile(t, stray); got != "unrelated file" {
			t.Errorf("%s holds %q, want the stray file untouched", stray, got)
		}
	}

	for _, path := range []string{db, filepath.Join(s.Dir, "attn.pid")} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	writeRestoreFile(t, db+"-wal", "stray wal")
	writeRestoreFile(t, db+"-shm", "stray shm")
	fresh := s.Attn("db", "restore", older)
	if from, preserved := restoredLines(t, fresh); from != older || preserved != "" || !strings.Contains(fresh.Stdout, "no previous attn.db existed to preserve") {
		t.Errorf("a restore with no attn.db printed:\n%s", fresh.Stdout)
	}
	if got := readRestoreFile(t, db); got != "older snapshot" {
		t.Errorf("attn.db holds %q, want the chosen snapshot", got)
	}
	assertNoRestoreFile(t, db+"-wal", db+"-shm")
}
