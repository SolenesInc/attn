package main_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestARingHeldThroughTheUpgradeReachesTheChiefOnceAfterItsNotebookComesBack(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.TrustedApp()
	if set := s.Attn("crew", "set", "chief", "--agent", "claude"); set.Code != 0 {
		t.Fatalf("crew set chief: %+v", set)
	}
	var woken protocol.CrewWakeResult
	s.Attn("crew", "start", "chief", "--json").JSON(t, &woken)
	legacy := string(woken.SessionID)
	run := s.Launched(legacy)
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)
	run.Prompted()
	running := testworld.AwaitSession(app, legacy, func(protocol.Session) bool { return true })
	run.Reply("Ready. <!-- attn:state=idle -->")
	run.Input("write the essay")
	if got := run.Prompted(); got != "write the essay" {
		t.Fatalf("the Chief took %q, want the user's prompt", got)
	}
	testworld.AwaitSession(app, legacy, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	const words = "check the rounding"
	capture := uuid.NewString()
	member := "chief"
	if _, err := app.QuickCapture(protocol.QuickCaptureSendMessage{
		Cmd: protocol.CmdQuickCaptureSend, CaptureID: capture, Content: words, AttachmentIds: []string{},
		Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindCrewMember, MemberID: &member},
	}); err != nil {
		t.Fatal(err)
	}
	for {
		event := run.ModNext()
		if event.Kind == fakeagent.ModEventHeld && strings.Contains(event.Text, words) {
			break
		}
	}
	testworld.Await(app, protocol.EventQuickCaptureRead, func(m protocol.QuickCaptureReadMessage) bool { return m.CaptureID == capture })

	got := s.Attn("settings", "get", "notebook.root.effective")
	if got.Code != 0 {
		t.Fatalf("read the Notebook folder: %+v", got)
	}
	homes := filepath.Join(strings.TrimSpace(got.Stdout), "crew")
	run.ModHeldRingsRecordAddress("chief:" + running.ProfileID)
	s.Stop()

	leaveChiefAsTheMigrationDoes(t, s.Dir, running.ProfileID, legacy)
	if err := os.RemoveAll(filepath.Join(homes, "chief")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(homes, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(homes, 0o755) })

	s.Start()
	app = s.TrustedApp()
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)
	if returned := run.ModEsc(); len(returned) != 1 {
		t.Fatalf("Esc returned %v, want the held Quick Capture", returned)
	}
	testworld.Await(app, protocol.EventQuickCaptureUnread, func(m protocol.QuickCaptureUnreadMessage) bool { return m.CaptureID == capture })
	s.Stop()

	if err := os.Chmod(homes, 0o755); err != nil {
		t.Fatal(err)
	}
	nextLaunch := s.NextLaunch()
	s.Start()
	app = s.TrustedApp()
	recreated := nextLaunch()
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)
	recreated.Prompted()
	recreated.Reply("Ready. <!-- attn:state=waiting_input -->")
	if got := recreated.Prompted(); !strings.Contains(got, words) {
		t.Fatalf("the recreated Chief took %q, want the returned Quick Capture", got)
	}
	awaitReadAsAConnectingAppLearnsIt(t, app, capture)
	recreated.Reply("Thanks. <!-- attn:state=idle -->")
	probe := "probe-" + uuid.NewString()[:8]
	recreated.Input(probe)
	if got := recreated.Prompted(); got != probe {
		t.Fatalf("the Chief took %q before %q, want the Quick Capture only once", got, probe)
	}
}

func awaitReadAsAConnectingAppLearnsIt(t *testing.T, app *testworld.Peer, capture string) {
	t.Helper()
	got, err := app.QuickCapture(protocol.QuickCaptureGetMessage{Cmd: protocol.CmdQuickCaptureGet, CaptureID: capture})
	if err != nil {
		t.Fatal(err)
	}
	if got.Record.ReadAt == nil {
		testworld.Await(app, protocol.EventQuickCaptureRead, func(m protocol.QuickCaptureReadMessage) bool { return m.CaptureID == capture })
	}
}

func leaveChiefAsTheMigrationDoes(t *testing.T, dir, profileID, session string) {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE profiles SET chief_session_id = ? WHERE id = ?`, session, profileID); err != nil {
		t.Fatal(err)
	}
	var collection int64
	if err := db.QueryRow(`SELECT id FROM document_collections WHERE namespace = ? AND collection = ?`, crew.Namespace, crew.CollectionMembers).Scan(&collection); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM ` + docstore.TableName(collection)); err != nil {
		t.Fatal(err)
	}
}
