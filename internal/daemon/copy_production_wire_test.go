package daemon_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestACopyOfProductionNeverRunsACopiedAgent(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	source := w.App()
	stopped := w.Spawn(source, fakeagent.Claude, w.Path("shop"))
	settleCopiedAgent(t, w, source, stopped)
	closed := w.Spawn(source, fakeagent.Claude, w.Path("shop"))
	settleCopiedAgent(t, w, source, closed)
	closeSessionPane(source, closed)
	awaitClosed(source, closed)

	copyProductionAndMoveWorldIntoIt(t, w)
	firstLaunch := w.NextLaunch()
	w.start()
	app := w.App()

	copiedSession(t, app, stopped)
	if revived := attachRevive(app, stopped, 80, 24); revived.Success {
		t.Errorf("attaching with revive started copied session %s: %+v", stopped, revived)
	}
	if reopened, err := w.Client().SessionReopen(client.SessionReopenOptions{SessionID: protocol.SessionID(closed)}); err == nil {
		t.Errorf("the ledger resumed copied session %s: %+v", closed, reopened)
	}

	fresh := w.Spawn(app, fakeagent.Claude, w.Path("scratch"))
	terminal := app.Terminal(fresh)
	if run := firstLaunch(); run.SessionID != terminal {
		t.Fatalf("the copy's first launch ran in terminal %s (conversation %s), want %s, the session started in the copy", run.SessionID, run.ConversationID, terminal)
	}
	settleCopiedAgent(t, w, app, fresh)

	afterRestart := w.NextLaunch()
	w.restart()
	if run := afterRestart(); run.SessionID != terminal || !run.Resumed {
		t.Fatalf("after a restart the copy launched terminal %s (resumed %v), want only %s, the session started in the copy, resumed", run.SessionID, run.Resumed, terminal)
	}
	copiedSession(t, w.App(), stopped)
}

func settleCopiedAgent(t *testing.T, w *world, app *testworld.Peer, session string) {
	t.Helper()
	run := w.Launched(session)
	app.TypeLine(session, "add a discount field")
	run.Prompted()
	run.Reply("Done. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func copiedSession(t *testing.T, app *testworld.Peer, id string) protocol.Session {
	t.Helper()
	for _, session := range app.Initial.Sessions {
		if string(session.ID) == id {
			return session
		}
	}
	t.Fatalf("the copy lost session %s: %+v", id, app.Initial.Sessions)
	return protocol.Session{}
}

func copyProductionAndMoveWorldIntoIt(t *testing.T, w *world) {
	t.Helper()
	w.stop()
	sourceDB := filepath.Join(w.Dir, "attn.db")
	dest := filepath.Join(w.Dir, "copy")
	resolver := filepath.Join(w.Dir, "copy-resolver")
	if err := os.WriteFile(resolver, []byte("#!/bin/sh\nprintf '%s\\n' \"$COPY_DEST\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("../../scripts/gardenpushperf/copy-production.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", string(script), "copy-production.sh", "copytest", sourceDB)
	cmd.Env = append(os.Environ(), "ATTN="+resolver, "COPY_DEST="+dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("copy-production.sh: %v\n%s", err, out)
	}
	t.Setenv("ATTN_DATA_DIR", dest)
	w.Dir = dest
	w.Socket = filepath.Join(dest, "attn.sock")
}
