package main_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestDatabaseRestorePreservesConversationArchivesFromAfterTheSnapshot(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	awaitBackup := func(app *testworld.Peer) {
		t.Helper()
		if backedUpAt, _ := app.Initial.Settings["db.last_backup_at"].(string); backedUpAt == "" {
			testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
				backedUpAt, _ := m.Settings["db.last_backup_at"].(string)
				return backedUpAt != ""
			})
		}
	}
	type conversation struct {
		state, native, source, archive string
		original, partial              []byte
	}
	var conversations []conversation
	for _, state := range []string{"missing", "deleted", "released"} {
		id := s.Spawn(app, fakeagent.Claude, s.Path(state))
		run := s.Launched(id)
		app.TypeLine(id, "Remember this conversation")
		run.Prompted()
		run.Reply("history before the backup <!-- attn:state=waiting_input -->")
		if closed := s.Attn("agent", "close", id, "-m", "finished", "--source-session", id); closed.Code != 0 {
			t.Fatalf("close: %+v", closed)
		}
		testworld.AwaitTaskDone(app, "conversation_keep")
		if state != "missing" {
			if err := s.Client().KeptConversationKeep(run.ConversationID, true); err != nil {
				t.Fatal(err)
			}
			testworld.AwaitTaskDone(app, "conversation_keep")
			if state == "deleted" {
				if err := s.Client().KeptConversationForget(run.ConversationID); err != nil {
					t.Fatal(err)
				}
			} else if err := s.Client().KeptConversationKeep(run.ConversationID, false); err != nil {
				t.Fatal(err)
			}
			testworld.AwaitTaskDone(app, "conversation_keep")
		}
		paths, err := filepath.Glob(filepath.Join(s.Dir, "toolhome", ".claude", "projects", "*", run.ConversationID+".jsonl"))
		if err != nil || len(paths) != 1 {
			t.Fatalf("native transcript: %v, %v", paths, err)
		}
		conversations = append(conversations, conversation{state: state, native: run.ConversationID, source: paths[0], archive: filepath.Join(s.Dir, "conversations", "claude", run.ConversationID+".tar.zst")})
	}
	snapshotRows, err := s.Client().KeptConversationList(true)
	if err != nil || len(snapshotRows.Rows) != 2 {
		t.Fatalf("snapshot conversation rows: %+v, %v", snapshotRows, err)
	}
	awaitBackup(app)
	s.Stop()
	if err := os.Rename(filepath.Join(s.Dir, "backups"), filepath.Join(s.Dir, "earlier-backups")); err != nil {
		t.Fatal(err)
	}
	s.Start()
	app = s.App()
	awaitBackup(app)
	backups, err := filepath.Glob(filepath.Join(s.Dir, "backups", "attn-*.db"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("database backup before re-keep: %v, %v", backups, err)
	}
	snapshot := backups[0]
	for i := range conversations {
		c := &conversations[i]
		before, err := os.ReadFile(c.source)
		if err != nil {
			t.Fatal(err)
		}
		c.partial = append(before, []byte("{\"type\":\"summary\",\"summary\":\"partial\"}\n")...)
		file, err := os.OpenFile(c.source, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.WriteString("{\"type\":\"summary\",\"summary\":\"history written after the backup\"}\n")
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatalf("extend native transcript: %v, %v", writeErr, closeErr)
		}
		if err := s.Client().KeptConversationKeep(c.native, true); err != nil {
			t.Fatal(err)
		}
		testworld.AwaitTaskDone(app, "conversation_keep")
		c.original, err = os.ReadFile(c.archive)
		if err != nil || len(c.original) == 0 {
			t.Fatalf("%s kept archive: %d bytes, %v", c.state, len(c.original), err)
		}
	}
	s.Stop()
	if restored := s.Attn("db", "restore", snapshot); restored.Code != 0 {
		t.Fatalf("restore: %+v", restored)
	} else if !strings.Contains(restored.Stdout, "known archives are pinned until you unkeep or forget them") {
		t.Fatalf("restore did not explain archive retention: %+v", restored)
	}
	for _, c := range conversations {
		if c.state == "released" {
			if err := os.WriteFile(c.source, c.partial, 0600); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Remove(c.source); err != nil {
			t.Fatal(err)
		}
	}
	s.StartWith("ATTN_CONVERSATION_KEEP_GRACE_DAYS=0")
	app = s.App()
	for _, pass := range []string{"first", "second"} {
		plant(t, s, pass+" conversation keep sweep after restore")
		testworld.AwaitTaskDone(app, "conversation_keep")
	}
	for _, c := range conversations {
		if got, err := os.ReadFile(c.archive); err != nil || !bytes.Equal(got, c.original) {
			t.Errorf("%s archive after restore and sweep: %d bytes, %v; want original %d bytes", c.state, len(got), err, len(c.original))
		}
	}
	rows, err := s.Client().KeptConversationList(false)
	if err != nil || len(rows.Rows) != 2 {
		t.Fatalf("restored conversation list: %+v, %v", rows, err)
	}
	for _, row := range rows.Rows {
		if row.Kept == nil || row.Kept.PinnedAt == nil || row.Kept.DeletedAt != nil || row.Kept.DeleteAfter != nil {
			t.Fatalf("restored archive must stay pinned: %+v", row)
		}
		if err := s.Client().KeptConversationKeep(row.ResumeID, false); err != nil {
			t.Fatal(err)
		}
		testworld.AwaitTaskDone(app, "conversation_keep")
	}
	plant(t, s, "expire archives explicitly released after restore")
	testworld.AwaitTaskDone(app, "conversation_keep")
	for _, c := range conversations {
		if c.state == "missing" {
			continue
		}
		if _, err := os.Stat(c.archive); !os.IsNotExist(err) {
			t.Errorf("%s archive survived explicit release: %v", c.state, err)
		}
	}
}

func TestKeptConversationSurvivesDaemonRestartAndResumeRestoresIt(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	cwd := s.Path("api")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	delegated, err := s.Client().Delegate(protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "keep-conversation", Cwd: cwd, Agent: protocol.Ptr("claude"),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Keep this conversation until the work is done.")},
	})
	if err != nil {
		t.Fatal(err)
	}
	first := s.Launched(string(delegated.SessionID))
	first.Prompted()
	shared := plant(t, s, "Keep another piece of this conversation")
	if tended := s.Run(testworld.Invocation{Args: []string{"seed", "tend", shared.ID}, Session: string(delegated.SessionID)}); tended.Code != 0 {
		t.Fatalf("tend shared conversation: %+v", tended)
	}
	first.Reply("remember this <!-- attn:state=waiting_input -->")
	paths, err := filepath.Glob(filepath.Join(s.Dir, "toolhome", ".claude", "projects", "*", first.ConversationID+".jsonl"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("transcript paths: %v, %v", paths, err)
	}
	path := paths[0]
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	closed := s.Attn("agent", "close", string(delegated.SessionID), "-m", "verify durable conversation", "--source-session", string(delegated.SessionID))
	if closed.Code != 0 {
		t.Fatalf("close: %+v", closed)
	}
	testworld.AwaitTaskDone(s.App(), "conversation_keep")
	shown := s.Attn("seed", "show", delegated.SeedID).Stdout
	if !strings.Contains(shown, "conversation  kept by attn") || !strings.Contains(shown, "while an open seed points at it") {
		t.Fatalf("seed show:\n%s", shown)
	}
	if closedSeed := s.Attn("seed", "wither", shared.ID, "-m", "This piece is done"); closedSeed.Code != 0 {
		t.Fatalf("wither shared seed: %+v", closedSeed)
	}
	testworld.AwaitTaskDone(s.App(), "conversation_keep")
	closedShow := s.Attn("seed", "show", shared.ID).Stdout
	if !strings.Contains(closedShow, "while an open seed points at it") || strings.Contains(closedShow, "while this seed is open") {
		t.Fatalf("closed seed must describe the other open reference:\n%s", closedShow)
	}
	s.Stop()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	s.Start()
	result := testworld.Request(s.App(), protocol.SeedResumeMessage{
		Cmd: protocol.CmdSeedResume, SeedID: delegated.SeedID, RequestID: protocol.Ptr("resume-kept"),
	}, protocol.EventSeedResumeResult, func(result protocol.SeedResumeResultMessage) bool { return result.RequestID == "resume-kept" })
	if !result.Success {
		t.Fatalf("resume: %+v", result)
	}

	resumed := s.Launched(string(delegated.SessionID))
	if !resumed.Resumed || resumed.ConversationID != first.ConversationID {
		t.Fatalf("resumed conversation: %+v", resumed)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatalf("restored transcript = %q, %v; want %q", got, err, original)
	}
}

func TestConversationCLIListPinUnkeepAndForget(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	cwd := s.Path("api")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	delegated, err := s.Client().Delegate(protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "conversation-cli", Cwd: cwd, Agent: protocol.Ptr("claude"),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("A small kept conversation")},
	})
	if err != nil {
		t.Fatal(err)
	}
	first := s.Launched(string(delegated.SessionID))
	first.Prompted()
	app := s.App()

	if result := s.Attn("conversation", "keep", string(delegated.SessionID)); result.Code != 0 {
		t.Fatalf("live pin: %+v", result)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	pending := s.Attn("conversation", "list")
	if pending.Code != 0 || !strings.Contains(pending.Stdout, "0 kept · 0 B · 1 pending") || !strings.Contains(pending.Stdout, "copy once quiet") || !strings.Contains(pending.Stdout, first.ConversationID) {
		t.Fatalf("pending list: %+v", pending)
	}
	if result := s.Attn("conversation", "unkeep", string(delegated.SessionID)); result.Code != 0 {
		t.Fatalf("live unpin: %+v", result)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	if result := s.Attn("agent", "close", string(delegated.SessionID), "-m", "done", "--source-session", string(delegated.SessionID)); result.Code != 0 {
		t.Fatalf("close: %+v", result)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	shown := s.Attn("seed", "show", delegated.SeedID)
	if shown.Code != 0 || strings.Contains(shown.Stdout, "0.0 MB") || !strings.Contains(shown.Stdout, "kept by attn (") || !(strings.Contains(shown.Stdout, " B)") || strings.Contains(shown.Stdout, " KB)")) {
		t.Fatalf("small archive display: %+v", shown)
	}
	for _, args := range [][]string{{"conversation", "keep", first.ConversationID}, {"conversation", "unkeep", string(delegated.SessionID)}, {"conversation", "keep", string(delegated.SessionID)}} {
		if result := s.Attn(args...); result.Code != 0 {
			t.Fatalf("%v: %+v", args, result)
		}
		testworld.AwaitTaskDone(app, "conversation_keep")
	}
	list := s.Attn("conversation", "list")
	for _, want := range []string{"1 kept · ", "CONVERSATION", first.ConversationID, "claude", "pinned ", "open seed ", delegated.SeedID, string(delegated.SessionID)} {
		if list.Code != 0 || !strings.Contains(list.Stdout, want) {
			t.Errorf("list missing %q: %+v", want, list)
		}
	}
	if strings.Contains(list.Stdout, "0.0 MB") {
		t.Fatalf("small list size rounded to zero: %+v", list)
	}
	refused := s.Attn("conversation", "forget", string(delegated.SessionID))
	if refused.Code == 0 || !strings.Contains(refused.Stderr, delegated.SeedID) {
		t.Fatalf("open seed forget: %+v", refused)
	}
	if result := s.Attn("seed", "wither", delegated.SeedID, "-m", "finished"); result.Code != 0 {
		t.Fatalf("wither: %+v", result)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	if result := s.Attn("conversation", "forget", first.ConversationID); result.Code != 0 || !strings.Contains(result.Stdout, "Claude's own files are untouched") {
		t.Fatalf("forget: %+v", result)
	}
	list = s.Attn("conversation", "list")
	if list.Code != 0 || !strings.Contains(list.Stdout, "0 kept · 0 B") || strings.Contains(list.Stdout, first.ConversationID) {
		t.Fatalf("live list: %+v", list)
	}
	all := s.Attn("conversation", "list", "--all")
	if all.Code != 0 || !strings.Contains(all.Stdout, first.ConversationID) || !strings.Contains(all.Stdout, "you deleted ") {
		t.Fatalf("tombstone list: %+v", all)
	}
	shown = s.Attn("seed", "show", delegated.SeedID)
	if shown.Code != 0 || !strings.Contains(shown.Stdout, "you deleted attn's copy") {
		t.Fatalf("seed user tombstone: %+v", shown)
	}
}
