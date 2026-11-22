package daemon_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func countActivityWrites(t *testing.T, w *world, session string) func() int {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(w.Dir, "attn.db")+"?mode=rw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE activity_writes(session_id TEXT, last_seen TEXT);
		CREATE TRIGGER activity_write AFTER UPDATE OF last_seen ON session_runtime
		WHEN old.last_seen IS NOT new.last_seen
		BEGIN
			INSERT INTO activity_writes VALUES (new.session_id, new.last_seen);
		END;
	`); err != nil {
		t.Fatal(err)
	}
	return func() int {
		t.Helper()
		var writes int
		if err := db.QueryRow("SELECT count(*) FROM activity_writes WHERE session_id = ?", session).Scan(&writes); err != nil {
			t.Fatal(err)
		}
		return writes
	}
}

func TestAPromptTheHookReportsWritesItsSessionsActivityOnce(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(session)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	writes := countActivityWrites(t, w, session)

	terminal := protocol.TerminalID(w.Terminal(session))
	if err := w.Client().UpdateStateFromHookEvidence(terminal, protocol.StateWorking, "", "user_prompt_submit", "check the project"); err != nil {
		t.Fatal(err)
	}
	if got := writes(); got != 1 {
		t.Fatalf("one submitted prompt wrote the session's activity %d times before its hook was answered, want 1", got)
	}
	if err := w.Client().UpdateStateFromHookEvidence(terminal, protocol.StateIdle, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if got := writes(); got != 2 {
		t.Fatalf("a hook with no prompt left the activity writes at %d, want it to write once more", got)
	}
}

func TestAPromptTheAppServerReportsWritesItsSessionsActivityOnce(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newTitlingWorld(t, h, fakeagent.Claude)
		app := w.App()
		session, view, server := appServerSession(t, w, app, h)
		writes := countActivityWrites(t, w, session)
		promptUntilWorking(app, session, view, server, "the login form rejects valid passwords")
		w.HeadlessTask().Answer("Fix login flow")
		awaitLabel(app, session, "Fix login flow")
		if got := writes(); got != 1 {
			t.Fatalf("one prompt the %s server reported wrote the session's activity %d times, want 1", h, got)
		}
	})
}
