package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
	"github.com/victorarias/attn/internal/transcript"
)

func conversationRows(t *testing.T, cli *client.Client, deleted bool) *protocol.KeptConversationListResult {
	t.Helper()
	result, err := cli.KeptConversationList(deleted)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func awaitConversationsChanged(app *testworld.Peer) {
	testworld.Await(app, protocol.EventKeptConversationsChanged, func(protocol.KeptConversationsChangedEvent) bool { return true })
}

func TestConversationKeepPassNotifiesOnceForMultipleCopies(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	var paths []string
	for _, name := range []string{"one", "two"} {
		delegated := seedResumeDelegate(t, w, fakeagent.Claude, name)
		run := w.Launched(string(delegated.SessionID))
		run.Prompted()
		run.Reply("source answer <!-- attn:state=waiting_input -->")
		paths = append(paths, transcript.FindClaudeTranscript(run.ConversationID))
		closePane(app, sessionPane{session: string(delegated.SessionID)})
		testworld.AwaitTaskDone(app, "conversation_keep")
	}
	before := conversationRows(t, cli, false)
	if before.Count != len(paths) {
		t.Fatalf("initial copies: %+v", before)
	}
	for _, path := range paths {
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.WriteString("{\"type\":\"summary\",\"summary\":\"additional source content\"}\n")
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatalf("extend native transcript: %v, %v", writeErr, closeErr)
		}
	}
	events := w.App()
	plantSeedAs(t, cli, "", "refresh both changed copies")
	testworld.AwaitTaskDone(events, "conversation_keep")
	listed := testworld.Request(events, protocol.KeptConversationListMessage{Cmd: protocol.CmdKeptConversationList, RequestID: protocol.Ptr("after-pass")}, protocol.EventKeptConversationListResult, func(r protocol.KeptConversationListResultEvent) bool {
		return protocol.Deref(r.RequestID) == "after-pass"
	})
	if !listed.Success || listed.KeptConversationListResult == nil || listed.KeptConversationListResult.Count != len(paths) || listed.KeptConversationListResult.StoredBytes <= before.StoredBytes {
		t.Fatalf("updated copies: %+v", listed)
	}
	for _, row := range listed.KeptConversationListResult.Rows {
		for _, old := range before.Rows {
			if row.ResumeID == old.ResumeID && protocol.Deref(row.SourceBytes) <= protocol.Deref(old.SourceBytes) {
				t.Fatalf("conversation %s was not recopied: before=%+v, after=%+v", row.ResumeID, old, row)
			}
		}
	}
	changed := 0
	for _, event := range events.Received() {
		if event.Event == protocol.EventKeptConversationsChanged {
			changed++
		}
	}
	if changed != 1 {
		t.Fatalf("multi-copy keep pass sent %d kept_conversations_changed events; want 1", changed)
	}
}

func TestConversationPinCopiesUnreferencedClosedSessionAndUnkeepExpires(t *testing.T) {
	t.Setenv("ATTN_CONVERSATION_KEEP_GRACE_DAYS", "0")
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	first := w.Launched(string(delegated.SessionID))
	first.Prompted()
	lifeMove(t, cli, string(delegated.SessionID), delegated.SeedID, "wither", "finished", "")
	testworld.AwaitTaskDone(app, "conversation_keep")
	closePane(app, sessionPane{session: string(delegated.SessionID)})
	testworld.AwaitTaskDone(app, "conversation_keep")
	if rows := conversationRows(t, cli, false); len(rows.Rows) != 0 {
		t.Fatalf("unreferenced closed session was copied: %+v", rows)
	}
	pinEvents := w.App()
	if err := cli.KeptConversationKeep(first.ConversationID, true); err != nil {
		t.Fatal(err)
	}
	awaitConversationsChanged(pinEvents)
	testworld.AwaitTaskDone(app, "conversation_keep")
	awaitConversationsChanged(pinEvents)
	pinned := conversationRows(t, cli, false)
	if len(pinned.Rows) != 1 || pinned.Rows[0].Kept.PinnedAt == nil || len(pinned.Rows[0].Seeds) != 0 || pinned.Rows[0].Kept.DeleteAfter != nil {
		t.Fatalf("pinned copy: %+v", pinned)
	}
	if pinned.Count != 1 || pinned.StoredBytes != pinned.Rows[0].Kept.Bytes || pinned.NextDeleteAfter != nil {
		t.Fatalf("pinned totals: %+v", pinned)
	}
	if err := cli.KeptConversationKeep(string(delegated.SessionID), false); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	released := conversationRows(t, cli, false)
	if released.Rows[0].Kept.PinnedAt != nil || released.Rows[0].Kept.DeleteAfter == nil || protocol.Deref(released.NextDeleteAfter) != *released.Rows[0].Kept.DeleteAfter {
		t.Fatalf("unkeep grace: %+v", released)
	}
	if _, err := cli.SeedNote("", delegated.SeedID, "trigger next keep pass", "", false, nil); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	if rows := conversationRows(t, cli, false); rows.Count != 0 || len(rows.Rows) != 0 || rows.StoredBytes != 0 || rows.NextDeleteAfter != nil {
		t.Fatalf("expired totals: %+v", rows)
	}
	deleted := conversationRows(t, cli, true)
	if len(deleted.Rows) != 1 || protocol.Deref(deleted.Rows[0].Kept.DeletedBy).Ref != "attn" {
		t.Fatalf("sweep tombstone: %+v", deleted)
	}
}

func TestConversationPinSurvivesSeedWitherAndRestart(t *testing.T) {
	t.Setenv("ATTN_CONVERSATION_KEEP_GRACE_DAYS", "0")
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	first := w.Launched(string(delegated.SessionID))
	first.Prompted()
	closePane(app, sessionPane{session: string(delegated.SessionID)})
	testworld.AwaitTaskDone(app, "conversation_keep")
	pin := testworld.Request(app, protocol.KeptConversationKeepMessage{Cmd: protocol.CmdKeptConversationKeep, SessionID: string(delegated.SessionID), Keep: true, RequestID: protocol.Ptr("pin")}, protocol.EventKeptConversationKeepResult, func(r protocol.KeptConversationKeepResultEvent) bool { return protocol.Deref(r.RequestID) == "pin" })
	if !pin.Success {
		t.Fatalf("pin: %+v", pin)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	pinnedAt := protocol.Deref(conversationRows(t, cli, false).Rows[0].Kept.PinnedAt)
	lifeMove(t, cli, "", delegated.SeedID, "wither", "finished", "")
	testworld.AwaitTaskDone(app, "conversation_keep")
	plantSeedAs(t, cli, "", "pass beyond zero grace")
	testworld.AwaitTaskDone(app, "conversation_keep")
	w.restart()
	list := testworld.Request(w.App(), protocol.KeptConversationListMessage{Cmd: protocol.CmdKeptConversationList, RequestID: protocol.Ptr("list")}, protocol.EventKeptConversationListResult, func(r protocol.KeptConversationListResultEvent) bool { return protocol.Deref(r.RequestID) == "list" })
	if !list.Success || list.KeptConversationListResult == nil {
		t.Fatalf("list: %+v", list)
	}
	rows := list.KeptConversationListResult.Rows
	if len(rows) != 1 || protocol.Deref(rows[0].Kept.PinnedAt) != pinnedAt || rows[0].Kept.DeleteAfter != nil || rows[0].Kept.DeletedAt != nil {
		t.Fatalf("durable pin: %+v", rows)
	}
}

func TestConversationForgetRefusesOpenSeedsThenDeletesOnlyAttnsCopy(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	first := w.Launched(string(delegated.SessionID))
	first.Prompted()
	closePane(app, sessionPane{session: string(delegated.SessionID)})
	testworld.AwaitTaskDone(app, "conversation_keep")
	path := transcript.FindClaudeTranscript(first.ConversationID)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cli.KeptConversationForget(string(delegated.SessionID)); err == nil || !strings.Contains(err.Error(), delegated.SeedID) || !strings.Contains(err.Error(), "harvest or wither") {
		t.Fatalf("open reference refusal: %v", err)
	}
	if err := cli.KeptConversationKeep(string(delegated.SessionID), true); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	lifeMove(t, cli, "", delegated.SeedID, "wither", "finished", "")
	testworld.AwaitTaskDone(app, "conversation_keep")
	archive := filepath.Join(w.Dir, "conversations", "claude", first.ConversationID+".tar.zst")
	archiveBytes, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	events := w.App()
	forgot := testworld.Request(app, protocol.KeptConversationForgetMessage{Cmd: protocol.CmdKeptConversationForget, SessionID: first.ConversationID, RequestID: protocol.Ptr("forget")}, protocol.EventKeptConversationForgetResult, func(r protocol.KeptConversationForgetResultEvent) bool {
		return protocol.Deref(r.RequestID) == "forget"
	})
	if !forgot.Success {
		t.Fatalf("forget: %+v", forgot)
	}
	awaitConversationsChanged(events)
	if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
		t.Fatalf("forget touched native transcript: %q, %v", got, err)
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("archive survived forget: %v", err)
	}
	if rows := conversationRows(t, cli, false); rows.Count != 0 || len(rows.Rows) != 0 {
		t.Fatalf("live forgotten copy: %+v", rows)
	}
	tombstone := conversationRows(t, cli, true).Rows[0].Kept
	if protocol.Deref(tombstone.DeletedBy).Ref != "user" || tombstone.PinnedAt != nil || conversationDateForTest(protocol.Deref(tombstone.DeletedAt)) != time.Now().UTC().Format("2006-01-02") {
		t.Fatalf("user tombstone: %+v", tombstone)
	}
	// Model a crash after the committed deletion but before its archive unlink.
	if err := os.WriteFile(archive, archiveBytes, 0600); err != nil {
		t.Fatal(err)
	}
	w.restart()
	app, cli = w.App(), w.Client()
	plantSeedAs(t, cli, "", "drain crash recovery")
	testworld.AwaitTaskDone(app, "conversation_keep")
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("restart left forgotten archive: %v", err)
	}
	if rows := conversationRows(t, cli, false); rows.Count != 0 || rows.PendingCount != 0 {
		t.Fatalf("restart recreated forgotten conversation: %+v", rows)
	}
	resumed, _, _ := w.RequestSpawn(app, fakeagent.Claude, w.Path("native-resume"), func(msg *protocol.SpawnSessionMessage) { msg.ResumeSessionID = protocol.Ptr(first.ConversationID) })
	if !resumed.Success {
		t.Fatalf("native files should still resume after forget: %+v", resumed)
	}
	next := w.Launched(string(resumed.ID))
	if !next.Resumed || next.ConversationID != first.ConversationID {
		t.Fatalf("native resume: %+v", next)
	}
	closePane(app, sessionPane{session: string(resumed.ID)})
	testworld.AwaitTaskDone(app, "conversation_keep")
	if rows := conversationRows(t, cli, false); len(rows.Rows) != 0 {
		t.Fatalf("forget left pin: %+v", rows)
	}
	sessionRecoveryDeleteTranscript(t, first.ConversationID)
	if err := cli.KeptConversationKeep(first.ConversationID, true); err == nil || !strings.Contains(err.Error(), "nothing left to keep") || !strings.Contains(err.Error(), first.ConversationID) {
		t.Fatalf("missing conversation pin: %v", err)
	}
	if rows := conversationRows(t, cli, false); rows.PendingCount != 0 {
		t.Fatalf("missing conversation left a pending pin: %+v", rows)
	}
	verdict := reopenVerdict(t, cli, string(delegated.SessionID))
	date := conversationDateForTest(protocol.Deref(tombstone.DeletedAt))
	if verdict.Reopenable || !strings.Contains(protocol.Deref(verdict.Reason), "you deleted attn's copy") || !strings.Contains(protocol.Deref(verdict.Reason), date) {
		t.Fatalf("forget resume refusal: %+v", verdict)
	}
	plantSeedAs(t, cli, "", "verify forget removed the pin")
	testworld.AwaitTaskDone(app, "conversation_keep")
	if rows := conversationRows(t, cli, false); len(rows.Rows) != 0 {
		t.Fatalf("forget left pin: %+v", rows)
	}
}

func conversationDateForTest(at string) string { return strings.Split(at, "T")[0] }

func TestConversationKeepAndForgetExplainNonKeeperHarness(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	id := w.Spawn(app, fakeagent.Codex, w.Path("codex"))
	run := w.Launched(id)
	app.TypeLine(id, "hello")
	run.Prompted()
	run.Reply("hello <!-- attn:state=waiting_input -->")
	for _, operation := range []func() error{func() error { return cli.KeptConversationKeep(id, true) }, func() error { return cli.KeptConversationKeep(id, false) }, func() error { return cli.KeptConversationForget(id) }} {
		if err := operation(); err == nil || !strings.Contains(err.Error(), "codex keeps its own conversations; attn has nothing to keep") {
			t.Fatalf("non-keeper refusal: %v", err)
		}
	}
}

func TestConversationIdentifiersRefuseAmbiguityAndListTotalsCountOnlyLiveCopies(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	var originals []string
	total := 0
	for _, name := range []string{"one", "two"} {
		delegated := seedResumeDelegate(t, w, fakeagent.Claude, name)
		run := w.Launched(string(delegated.SessionID))
		run.Prompted()
		run.Reply("on it <!-- attn:state=waiting_input -->")
		closePane(app, sessionPane{session: string(delegated.SessionID)})
		testworld.AwaitTaskDone(app, "conversation_keep")
		lifeMove(t, cli, "", delegated.SeedID, "wither", "finished", "")
		testworld.AwaitTaskDone(app, "conversation_keep")
		if err := cli.KeptConversationKeep(string(delegated.SessionID), true); err != nil {
			t.Fatal(err)
		}
		testworld.AwaitTaskDone(app, "conversation_keep")
		originals = append(originals, run.ConversationID)
	}
	before := conversationRows(t, cli, false)
	for _, row := range before.Rows {
		total += row.Kept.Bytes
	}
	if before.Count != 2 || before.StoredBytes != total {
		t.Fatalf("two-copy totals: %+v, sum=%d", before, total)
	}
	collision := w.Spawn(app, fakeagent.Codex, w.Path("collision"), func(msg *protocol.SpawnSessionMessage) { msg.ID = protocol.SessionID(originals[0]) })
	run := w.Launched(collision)
	app.TypeLine(collision, "identify")
	run.Prompted()
	run.Reply("done <!-- attn:state=waiting_input -->")
	if err := cli.KeptConversationKeep(originals[0], true); err == nil || !strings.Contains(err.Error(), "ambiguous identifier") || !strings.Contains(err.Error(), "claude conversation "+originals[0]) || !strings.Contains(err.Error(), "codex conversation "+run.ConversationID) {
		t.Fatalf("ambiguous identifier: %v", err)
	}
	if err := cli.KeptConversationForget(originals[1]); err != nil {
		t.Fatal(err)
	}
	live, all := conversationRows(t, cli, false), conversationRows(t, cli, true)
	if live.Count != 1 || len(live.Rows) != 1 || live.StoredBytes != live.Rows[0].Kept.Bytes || len(all.Rows) != 2 || all.Count != live.Count || all.StoredBytes != live.StoredBytes {
		t.Fatalf("live/deleted totals: live=%+v, all=%+v", live, all)
	}
	if err := cli.KeptConversationKeep(originals[1], true); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	again := conversationRows(t, cli, true)
	if again.Count != 2 {
		t.Fatalf("pin forgotten conversation should recopy native files: %+v", again)
	}
	for _, row := range again.Rows {
		if row.ResumeID == originals[1] && (row.Kept.DeletedAt != nil || row.Kept.DeletedBy != nil || row.Kept.PinnedAt == nil) {
			t.Fatalf("recopy retains tombstone: %+v", row)
		}
	}
	if err := cli.KeptConversationKeep("session:"+originals[0], true); err == nil || !strings.Contains(err.Error(), "codex keeps its own conversations") {
		t.Fatalf("session: scope picks the session: %v", err)
	}
	if err := cli.KeptConversationKeep("conversation:"+originals[0], true); err != nil {
		t.Fatalf("conversation: scope picks the conversation: %v", err)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
}

func TestConversationLivePinIsVisibleBeforeCopyAndCanBeRemoved(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	id := w.Spawn(app, fakeagent.Claude, w.Path("live"))
	run := w.Launched(id)
	app.TypeLine(id, "stay live")
	run.Prompted()
	run.Reply("live <!-- attn:state=waiting_input -->")
	if err := cli.KeptConversationKeep(id, true); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	pending := conversationRows(t, cli, false)
	if pending.Count != 0 || pending.StoredBytes != 0 || pending.PendingCount != 1 || len(pending.Rows) != 1 {
		t.Fatalf("pending totals: %+v", pending)
	}
	row := pending.Rows[0]
	if row.Kept != nil || row.SourceBytes != nil || row.PinnedAt == nil || !strings.Contains(protocol.Deref(row.PendingReason), "quiet") {
		t.Fatalf("pending row: %+v", row)
	}
	shown, err := cli.SessionShow(protocol.SessionID(id))
	if err != nil || protocol.Deref(shown.Entry.ConversationPinnedAt) != *row.PinnedAt {
		t.Fatalf("live ledger pin: %+v, %v", shown, err)
	}
	listed, err := cli.SessionList(client.SessionListOptions{All: true})
	if err != nil || len(listed.Entries) != 1 || listed.Entries[0].ConversationPinnedAt == nil {
		t.Fatalf("ledger list pin: %+v, %v", listed, err)
	}
	if err := cli.KeptConversationKeep(id, false); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	if rows := conversationRows(t, cli, false); rows.PendingCount != 0 || len(rows.Rows) != 0 {
		t.Fatalf("unpin should remove pending row: %+v", rows)
	}
	shown, err = cli.SessionShow(protocol.SessionID(id))
	if err != nil || shown.Entry.ConversationPinnedAt != nil {
		t.Fatalf("ledger pin survived unkeep: %+v, %v", shown, err)
	}
}

func TestConversationSeedReferenceIsVisibleBeforeCopy(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	run := w.Launched(string(delegated.SessionID))
	run.Prompted()
	run.Reply("live <!-- attn:state=waiting_input -->")
	plantSeedAs(t, cli, "", "drain the keep pass")
	testworld.AwaitTaskDone(app, "conversation_keep")
	rows := conversationRows(t, cli, false)
	if rows.Count != 0 || rows.StoredBytes != 0 || rows.PendingCount != 1 || len(rows.Rows) != 1 {
		t.Fatalf("seed-only pending totals: %+v", rows)
	}
	row := rows.Rows[0]
	if row.Kept != nil || row.SourceBytes != nil || row.PinnedAt != nil || len(row.Seeds) != 1 || row.Seeds[0].ID != delegated.SeedID || len(row.SessionIds) != 1 || row.SessionIds[0] != delegated.SessionID || !strings.Contains(protocol.Deref(row.PendingReason), "quiet") || !strings.Contains(protocol.Deref(row.PendingReason), row.Seeds[0].Slug) {
		t.Fatalf("seed-only pending row: %+v", row)
	}
	lifeMove(t, cli, string(delegated.SessionID), delegated.SeedID, "wither", "finished", "")
	testworld.AwaitTaskDone(app, "conversation_keep")
	if rows := conversationRows(t, cli, false); rows.PendingCount != 0 || len(rows.Rows) != 0 {
		t.Fatalf("closed seed left a pending row: %+v", rows)
	}
}

func TestConversationPendingPinCanBeUnkeptAfterClear(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	id := w.Spawn(app, fakeagent.Claude, w.Path("live"))
	run := w.Launched(id)
	app.TypeLine(id, "first")
	run.Prompted()
	run.Reply("first <!-- attn:state=waiting_input -->")
	pinnedID := run.ConversationID
	if err := cli.KeptConversationKeep(id, true); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	clearClaude(app, run, id)
	awaitClosed(app, id)
	if err := cli.KeptConversationKeep(pinnedID, false); err != nil {
		t.Fatalf("pending pin should remain resolvable: %v", err)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	rows := conversationRows(t, cli, false)
	if rows.PendingCount != 0 {
		t.Fatalf("pending pin survived unkeep: %+v", rows)
	}
	for _, row := range rows.Rows {
		if row.PinnedAt != nil || (row.Kept != nil && row.Kept.PinnedAt != nil) {
			t.Fatalf("pin survived unkeep: %+v", row)
		}
	}
}
