package store

import (
	"fmt"
	"path/filepath"
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

func TestReadAgentMailboxCapsABatchAtTheMaximum(t *testing.T) {
	s := newAgentMailboxStore(t)
	base := time.Date(2026, 9, 3, 11, 0, 0, 0, time.UTC)
	for i := range 71 {
		id := fmt.Sprintf("item-%02d", i)
		if _, err := s.EnqueueMaintenancePrompt(id, "target", id, base.Add(time.Duration(i)*time.Nanosecond)); err != nil {
			t.Fatal(err)
		}
	}
	batch, remaining, err := s.ReadAgentMailbox("target", 500, base.Add(time.Minute))
	if err != nil || len(batch) != agentmailbox.MaxInboxLimit || remaining != 21 {
		t.Fatalf("maximum batch = %d items, %d remaining, %v", len(batch), remaining, err)
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

func TestMigration132SeparatesMailboxReceiptsAndPayloads(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := newSeededStore(dbPath)
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
