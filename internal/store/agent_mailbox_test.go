package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
)

func newAgentMailboxStore(t *testing.T) *Store {
	t.Helper()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func enqueuePeer(t *testing.T, s *Store, id, sender, recipient, body string, createdAt time.Time) agentmailbox.Delivery {
	t.Helper()
	delivery, err := s.EnqueuePeerMessage(agentmailbox.PeerMessage{
		ID: id, SenderSessionID: sender, Body: body,
		CreatedAt: createdAt.UTC().Format(sortableTimeFormat),
	}, recipient)
	if err != nil {
		t.Fatalf("EnqueuePeerMessage(%s): %v", id, err)
	}
	return delivery
}

func TestReadAgentMailboxReturnsABoundedFIFOAndExactReceipts(t *testing.T) {
	s := newAgentMailboxStore(t)
	base := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	enqueuePeer(t, s, "third", "sender", "target", "third body", base.Add(2*time.Second))
	enqueuePeer(t, s, "first", "sender", "target", "first body", base)
	enqueuePeer(t, s, "second", "sender", "target", "second body", base.Add(time.Second))
	enqueuePeer(t, s, "other-target", "sender", "elsewhere", "not yours", base)

	notifiedAt := base.Add(3 * time.Second)
	changed, err := s.MarkAgentMailboxNotified("target", notifiedAt)
	if err != nil || changed != 3 {
		t.Fatalf("MarkAgentMailboxNotified = %d, %v", changed, err)
	}
	unread, err := s.UnreadAgentMailboxDeliveries("target")
	if err != nil || len(unread) != 3 || unread[0].Item.ID != "first" || unread[2].Item.ID != "third" {
		t.Fatalf("unread inspection = %+v, %v", unread, err)
	}
	wantNotified := notifiedAt.Format(sortableTimeFormat)
	for _, delivery := range unread {
		if delivery.Item.NotifiedAt != wantNotified || delivery.Item.ReadAt != "" {
			t.Fatalf("unread receipt for %s = %q/%q", delivery.Item.ID,
				delivery.Item.NotifiedAt, delivery.Item.ReadAt)
		}
	}
	readAt := base.Add(4 * time.Second)
	items, remaining, err := s.ReadAgentMailbox("target", 2, readAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Item.ID != "first" || items[1].Item.ID != "second" || remaining != 1 {
		t.Fatalf("first batch = %+v, remaining %d", items, remaining)
	}
	if items[0].Peer == nil || items[0].Peer.Body != "first body" || items[1].Peer == nil || items[1].Peer.Body != "second body" {
		t.Fatalf("peer payloads = %+v", items)
	}
	wantRead := readAt.Format(sortableTimeFormat)
	for _, delivery := range items {
		if delivery.Item.NotifiedAt != wantNotified || delivery.Item.ReadAt != wantRead {
			t.Fatalf("receipts for %s = %q/%q, want %q/%q", delivery.Item.ID,
				delivery.Item.NotifiedAt, delivery.Item.ReadAt, wantNotified, wantRead)
		}
	}

	last, remaining, err := s.ReadAgentMailbox("target", 2, readAt.Add(time.Second))
	if err != nil || len(last) != 1 || last[0].Item.ID != "third" || remaining != 0 {
		t.Fatalf("second batch = %+v, remaining %d, err %v", last, remaining, err)
	}
	if hasUnread, err := s.HasUnreadAgentMailboxItems("target"); err != nil || hasUnread {
		t.Fatalf("target unread = %v, %v", hasUnread, err)
	}
	if hasUnread, err := s.HasUnreadAgentMailboxItems("elsewhere"); err != nil || !hasUnread {
		t.Fatalf("other target unread = %v, %v", hasUnread, err)
	}
}

func TestAgentMailboxPeerFIFOUsesChronologicalOrderWithinASecond(t *testing.T) {
	s := newAgentMailboxStore(t)
	base := time.Date(2026, 9, 1, 10, 0, 0, 100_000_000, time.UTC)
	enqueue := func(id string, createdAt time.Time) {
		t.Helper()
		if _, err := s.EnqueuePeerMessage(agentmailbox.PeerMessage{
			ID: id, SenderSessionID: "sender", Body: id,
			CreatedAt: createdAt.Format(time.RFC3339Nano),
		}, "target"); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
	}

	enqueue("later", base.Add(time.Nanosecond))
	enqueue("earlier", base)

	items, remaining, err := s.ReadAgentMailbox("target", 1, base.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Item.ID != "earlier" || remaining != 1 {
		t.Fatalf("oldest unread peer = %+v (%d remaining), want earlier", items, remaining)
	}
	want := base.Format(sortableTimeFormat)
	if items[0].Item.CreatedAt != want || items[0].Peer == nil || items[0].Peer.CreatedAt != want {
		t.Fatalf("created_at = %q/%v, want %q", items[0].Item.CreatedAt, items[0].Peer, want)
	}
}

func TestAgentMailboxUnreadRecipientQueriesIncludeNotifiedItems(t *testing.T) {
	s := newAgentMailboxStore(t)
	now := time.Now()
	enqueuePeer(t, s, "target-message", "sender", "target", "landed", now)
	enqueuePeer(t, s, "other-message", "sender", "other", "waiting", now)
	if changed, err := s.MarkAgentMailboxNotified("target", now.Add(time.Second)); err != nil || changed != 1 {
		t.Fatalf("mark notified = %d, %v", changed, err)
	}
	recipients, err := s.TargetsWithUnreadAgentMailboxItems()
	if err != nil || fmt.Sprint(recipients) != "[other target]" {
		t.Fatalf("unread recipients = %v, %v", recipients, err)
	}
}

func TestReadPeerMessageRequiresItsRecipientAndNotification(t *testing.T) {
	s := newAgentMailboxStore(t)
	now := time.Now()
	enqueuePeer(t, s, "message", "sender", "target", "hello", now)

	if _, _, err := s.ReadPeerMessage("message", "someone-else", now); err != ErrPeerMessageNotFound {
		t.Fatalf("unauthorized read error = %v", err)
	}
	if _, _, err := s.ReadPeerMessage("message", "target", now); err != ErrPeerMessageNotNotified {
		t.Fatalf("queued read error = %v", err)
	}
	if changed, err := s.MarkAgentMailboxNotified("target", now); err != nil || changed != 1 {
		t.Fatalf("mark notified = %d, %v", changed, err)
	}
	record, changed, err := s.ReadPeerMessage("message", "target", now.Add(time.Second))
	if err != nil || !changed || record.State() != agentmailbox.StateRead || record.Message.Body != "hello" {
		t.Fatalf("read = %+v, %v, %v", record, changed, err)
	}
	again, changed, err := s.ReadPeerMessage("message", "target", now.Add(time.Hour))
	if err != nil || changed || again.ReadAt != record.ReadAt {
		t.Fatalf("repeated read = %+v, %v, %v", again, changed, err)
	}
}

func TestAgentMailboxReceiptTimestampsAreWriteOnce(t *testing.T) {
	s := newAgentMailboxStore(t)
	first := time.Now().Add(-time.Hour)
	if _, err := s.EnqueueMaintenancePrompt("once", "target", "hello", first); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.MarkAgentMailboxNotified("target", first); err != nil || changed != 1 {
		t.Fatalf("first notify = %d, %v", changed, err)
	}
	if changed, err := s.MarkAgentMailboxNotified("target", time.Now()); err != nil || changed != 0 {
		t.Fatalf("second notify = %d, %v", changed, err)
	}
	readTime := first.Add(time.Minute)
	items, remaining, err := s.ReadAgentMailbox("target", 1, readTime)
	if err != nil || len(items) != 1 || remaining != 0 {
		t.Fatalf("read = %+v, remaining %d, err %v", items, remaining, err)
	}
	again, remaining, err := s.ReadAgentMailbox("target", 1, time.Now())
	if err != nil || len(again) != 0 || remaining != 0 {
		t.Fatalf("repeated read = %+v, remaining %d, err %v", again, remaining, err)
	}

	var notifiedAt, readAt string
	if err := s.db.QueryRow(`SELECT notified_at, read_at FROM agent_mailbox_items WHERE id = 'once'`).Scan(&notifiedAt, &readAt); err != nil {
		t.Fatal(err)
	}
	wantNotified := first.UTC().Format(sortableTimeFormat)
	wantRead := readTime.UTC().Format(sortableTimeFormat)
	if notifiedAt != wantNotified || readAt != wantRead {
		t.Fatalf("receipts = %q/%q, want %q/%q", notifiedAt, readAt, wantNotified, wantRead)
	}
}

func TestAgentMailboxRejectsAReadWithoutNotificationProof(t *testing.T) {
	s := newAgentMailboxStore(t)
	_, err := s.db.Exec(`
		INSERT INTO agent_mailbox_items
			(id, recipient_session_id, kind, source_id, coalesce_key, hint, prompt,
			 created_at, notified_at, read_at)
		VALUES ('impossible', 'target', 'maintenance_prompt', '', '', '', 'hello',
		        '2026-01-01T00:00:00Z', '', '2026-01-01T00:01:00Z')
	`)
	if err == nil {
		t.Fatal("mailbox accepted read_at without notified_at")
	}
}

func TestDeleteQueuedPeerMessageLeavesHandledHistory(t *testing.T) {
	s := newAgentMailboxStore(t)
	now := time.Now()
	enqueuePeer(t, s, "queued", "sender", "target", "not launched", now)
	enqueuePeer(t, s, "handled", "sender", "handled-target", "already read", now.Add(time.Second))
	if changed, err := s.MarkAgentMailboxNotified("handled-target", now.Add(time.Second)); err != nil || changed != 1 {
		t.Fatalf("mark notified = %d, %v", changed, err)
	}
	if _, changed, err := s.ReadPeerMessage("handled", "handled-target", now.Add(2*time.Second)); err != nil || !changed {
		t.Fatal(err)
	}

	if err := s.DeleteQueuedPeerMessage("queued"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteQueuedPeerMessage("handled"); err != nil {
		t.Fatal(err)
	}
	var items, messages int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_mailbox_items`).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM peer_messages`).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if items != 1 || messages != 1 {
		t.Fatalf("rows = %d items/%d messages, want handled history only", items, messages)
	}
}

func TestPeerMessageGuardCountsScopeEachLimit(t *testing.T) {
	s := newAgentMailboxStore(t)
	now := time.Now()
	enqueuePeer(t, s, "recent-dupe", "sender", "target", "same words", now.Add(-2*time.Second))
	enqueuePeer(t, s, "old-dupe", "sender", "target", "stale words", now.Add(-time.Hour))
	enqueuePeer(t, s, "other-sender", "someone-else", "target", "same words", now.Add(-time.Second))

	counts, err := s.PeerMessageGuardCounts("sender", "target", "same words", now.Add(-10*time.Second), now.Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !counts.DuplicateFromSender || counts.FromSenderInWindow != 1 || counts.UnreadForRecipient != 3 {
		t.Fatalf("counts = %+v", counts)
	}

	stale, err := s.PeerMessageGuardCounts("sender", "target", "stale words", now.Add(-10*time.Second), now.Add(-30*time.Second))
	if err != nil || stale.DuplicateFromSender {
		t.Fatalf("stale duplicate = %+v, %v", stale, err)
	}
}

func TestReadAgentMailboxEnforcesDefaultAndMaximumBatchLimits(t *testing.T) {
	s := newAgentMailboxStore(t)
	base := time.Date(2026, 9, 3, 11, 0, 0, 0, time.UTC)
	for i := range 71 {
		id := fmt.Sprintf("item-%02d", i)
		if _, err := s.EnqueueMaintenancePrompt(id, "target", id, base.Add(time.Duration(i)*time.Nanosecond)); err != nil {
			t.Fatal(err)
		}
	}
	first, remaining, err := s.ReadAgentMailbox("target", 0, base.Add(time.Minute))
	if err != nil || len(first) != agentmailbox.DefaultInboxLimit || remaining != 51 {
		t.Fatalf("default batch = %d items, %d remaining, %v", len(first), remaining, err)
	}
	second, remaining, err := s.ReadAgentMailbox("target", 500, base.Add(2*time.Minute))
	if err != nil || len(second) != agentmailbox.MaxInboxLimit || remaining != 1 {
		t.Fatalf("maximum batch = %d items, %d remaining, %v", len(second), remaining, err)
	}
}

func TestEnqueueMaintenancePromptOnceIsIdempotentAndRefreshesCoalescedContent(t *testing.T) {
	s := newAgentMailboxStore(t)
	base := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	first, claimed, err := s.EnqueueMaintenancePromptOnce(
		"ticket-1", "target", "3", "legacy-ticket", "read through 3", base,
	)
	if err != nil || !claimed || first.Item.ID != "ticket-1" {
		t.Fatalf("first enqueue = %+v, %v, %v", first, claimed, err)
	}
	retried, claimed, err := s.EnqueueMaintenancePromptOnce(
		"ticket-1", "target", "4", "legacy-ticket", "read through 4", base.Add(time.Second),
	)
	if err != nil || claimed || retried.Item.SourceID != "4" || retried.Item.Prompt != "read through 4" {
		t.Fatalf("same-id retry = %+v, %v, %v", retried, claimed, err)
	}
	coalesced, claimed, err := s.EnqueueMaintenancePromptOnce(
		"ticket-2", "target", "5", "legacy-ticket", "read through 5", base.Add(2*time.Second),
	)
	if err != nil || claimed || coalesced.Item.ID != "ticket-1" ||
		coalesced.Item.SourceID != "5" || coalesced.Item.Prompt != "read through 5" || coalesced.Item.CreatedAt != first.Item.CreatedAt {
		t.Fatalf("coalesced enqueue = %+v, %v, %v", coalesced, claimed, err)
	}

	read, remaining, err := s.ReadAgentMailboxItems(
		"target", agentmailbox.KindMaintenancePrompt, "legacy-ticket", base.Add(3*time.Second),
	)
	if err != nil || read != 1 || remaining != 0 {
		t.Fatalf("adapter read = %d, remaining %d, %v", read, remaining, err)
	}
	afterRead, claimed, err := s.EnqueueMaintenancePromptOnce(
		"ticket-2", "target", "6", "legacy-ticket", "read through 6", base.Add(4*time.Second),
	)
	if err != nil || !claimed || afterRead.Item.ID != "ticket-2" {
		t.Fatalf("enqueue after read = %+v, %v, %v", afterRead, claimed, err)
	}
}

func TestReadAgentMailboxItemsReportsAllRemainingUnread(t *testing.T) {
	s := newAgentMailboxStore(t)
	base := time.Date(2026, 9, 3, 13, 0, 0, 0, time.UTC)
	if _, _, err := s.EnqueueMaintenancePromptOnce(
		"ticket", "target", "9", "legacy-ticket", "ticket inbox", base,
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EnqueueMaintenancePromptOnce(
		"present", "target", "round-1", "present-round-1", "present handback", base.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	read, remaining, err := s.ReadAgentMailboxItems(
		"target", agentmailbox.KindMaintenancePrompt, "legacy-ticket", base.Add(2*time.Second),
	)
	if err != nil || read != 1 || remaining != 1 {
		t.Fatalf("targeted read = %d, remaining %d, %v", read, remaining, err)
	}
	read, remaining, err = s.ReadAgentMailboxItems(
		"target", agentmailbox.KindMaintenancePrompt, "legacy-ticket", base.Add(3*time.Second),
	)
	if err != nil || read != 0 || remaining != 1 {
		t.Fatalf("repeated read = %d, remaining %d, %v", read, remaining, err)
	}
}

func TestAgentMailboxUnreadItemsSurviveReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mailbox.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 9, 3, 14, 0, 0, 0, time.UTC)
	if _, err := s.EnqueueMaintenancePrompt("durable", "target", "survives restart", createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkAgentMailboxNotified("target", createdAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	items, remaining, err := s.ReadAgentMailbox("target", 1, createdAt.Add(time.Minute))
	if err != nil || len(items) != 1 || items[0].Item.Prompt != "survives restart" || remaining != 0 {
		t.Fatalf("read after reopen = %+v, remaining %d, %v", items, remaining, err)
	}
}

func TestMigration132SeparatesMailboxReceiptsAndPayloads(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	if _, err := s.db.Exec(`
		DROP TABLE agent_mailbox_items;
		DROP TABLE peer_messages;
		CREATE TABLE agent_messages (
			id TEXT PRIMARY KEY, sender_session_id TEXT NOT NULL,
			target_session_id TEXT NOT NULL, content TEXT NOT NULL,
			created_at TEXT NOT NULL, delivered_at TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE garden_seed_bells (
			watcher_session_id TEXT NOT NULL, seed_id TEXT NOT NULL,
			event_kind TEXT NOT NULL, message_id TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL, PRIMARY KEY(watcher_session_id, seed_id)
		);
		INSERT INTO agent_messages VALUES
			('peer-queued', 'sender', 'target', 'queued body', '2026-01-01T00:00:00Z', ''),
			('peer-done', 'sender', 'target', 'done body', '2026-01-01T00:00:01Z', '2026-01-01T00:01:00Z'),
			('maintenance', '', 'target', 'sleep now', '2026-01-01T00:00:02Z', ''),
			('seed-queued', '', 'target', 'old prompt', '2026-01-01T00:00:03Z', ''),
			('seed-notified', '', 'target', 'old prompt', '2026-01-01T00:00:04Z', '2026-01-01T00:01:04Z');
		INSERT INTO garden_seed_bells VALUES
			('target', 's-queued', 'note', 'seed-queued', '2026-01-01T00:00:03Z'),
			('target', 's-notified', 'harvested', 'seed-notified', '2026-01-01T00:00:04Z');
		DELETE FROM schema_migrations WHERE version >= 132;
	`); err != nil {
		t.Fatalf("plant pre-132 schema: %v", err)
	}

	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}

	var unread int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM agent_mailbox_items
		WHERE recipient_session_id = 'target' AND read_at = ''
	`).Scan(&unread); err != nil {
		t.Fatal(err)
	}
	if unread != 4 {
		t.Fatalf("unread mailbox items = %d, want queued peer, maintenance and two Garden items", unread)
	}
	var peerNotified, peerRead, seedNotified, seedRead, maintenancePrompt string
	if err := s.db.QueryRow(`SELECT notified_at, read_at FROM agent_mailbox_items WHERE id = 'peer-done'`).Scan(&peerNotified, &peerRead); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT notified_at, read_at FROM agent_mailbox_items WHERE id = 'seed-notified'`).Scan(&seedNotified, &seedRead); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT prompt FROM agent_mailbox_items WHERE id = 'maintenance'`).Scan(&maintenancePrompt); err != nil {
		t.Fatal(err)
	}
	if peerNotified == "" || peerRead == "" || seedNotified == "" || seedRead != "" || maintenancePrompt != "sleep now" {
		t.Fatalf("migration receipts peer=%q/%q seed=%q/%q maintenance=%q",
			peerNotified, peerRead, seedNotified, seedRead, maintenancePrompt)
	}
	for _, table := range []string{"agent_messages", "garden_seed_bells"} {
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(new(int)); err == nil {
			t.Fatalf("legacy table %s survived migration", table)
		}
	}
}

func TestMigration133IndexesUnreadMailboxFIFO(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-133.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`
		DROP INDEX idx_agent_mailbox_recipient_unread;
		CREATE INDEX idx_agent_mailbox_recipient_queued
			ON agent_mailbox_items(recipient_session_id, notified_at, created_at, id);
		DELETE FROM schema_migrations WHERE version >= 133;
	`); err != nil {
		t.Fatalf("rewind migration 133: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}

	var oldIndexes int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_agent_mailbox_recipient_queued'
	`).Scan(&oldIndexes); err != nil {
		t.Fatal(err)
	}
	if oldIndexes != 0 {
		t.Fatal("queued mailbox index survived migration 133")
	}
	var indexSQL string
	if err := s.db.QueryRow(`
		SELECT sql FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_agent_mailbox_recipient_unread'
	`).Scan(&indexSQL); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"recipient_session_id, created_at, id", "WHERE read_at = ''"} {
		if !strings.Contains(indexSQL, want) {
			t.Fatalf("unread index %q does not contain %q", indexSQL, want)
		}
	}
}
