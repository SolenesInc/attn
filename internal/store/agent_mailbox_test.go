package store

import (
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

func TestAgentMailboxQueuesItemsUntilTheirNotificationIsStamped(t *testing.T) {
	s := newAgentMailboxStore(t)
	now := time.Now()
	enqueuePeer(t, s, "second", "sender", "target", "later", now)
	enqueuePeer(t, s, "first", "sender", "target", "earlier", now.Add(-time.Minute))
	enqueuePeer(t, s, "other-target", "sender", "elsewhere", "not yours", now)

	queued, err := s.QueuedAgentMailboxDeliveries("target")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 2 || queued[0].Item.ID != "first" || queued[1].Item.ID != "second" {
		t.Fatalf("queue is not oldest-first: %+v", queued)
	}
	if queued[0].Peer == nil || queued[0].Peer.Body != "earlier" || queued[0].Peer.SenderSessionID != "sender" {
		t.Fatalf("peer payload = %+v", queued[0].Peer)
	}
	if pending, err := s.AgentMailboxItemQueued("first"); err != nil || !pending {
		t.Fatalf("AgentMailboxItemQueued(first) = %v, %v", pending, err)
	}

	recipients, err := s.TargetsWithQueuedAgentMailboxItems()
	if err != nil || len(recipients) != 2 {
		t.Fatalf("queued recipients = %v, %v", recipients, err)
	}

	if err := s.MarkAgentMailboxItemHandled("first", now); err != nil {
		t.Fatal(err)
	}
	queued, err = s.QueuedAgentMailboxDeliveries("target")
	if err != nil || len(queued) != 1 || queued[0].Item.ID != "second" {
		t.Fatalf("after handling, queue = %+v, %v", queued, err)
	}
}

func TestAgentMailboxReceiptTimestampsAreWriteOnce(t *testing.T) {
	s := newAgentMailboxStore(t)
	first := time.Now().Add(-time.Hour)
	enqueuePeer(t, s, "once", "sender", "target", "hello", first)
	if err := s.MarkAgentMailboxItemHandled("once", first); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAgentMailboxItemHandled("once", time.Now()); err != nil {
		t.Fatal(err)
	}

	var notifiedAt, readAt string
	if err := s.db.QueryRow(`SELECT notified_at, read_at FROM agent_mailbox_items WHERE id = 'once'`).Scan(&notifiedAt, &readAt); err != nil {
		t.Fatal(err)
	}
	want := first.UTC().Format(sortableTimeFormat)
	if notifiedAt != want || readAt != want {
		t.Fatalf("receipts = %q/%q, want %q", notifiedAt, readAt, want)
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
	enqueuePeer(t, s, "handled", "sender", "target", "already read", now)
	if err := s.MarkAgentMailboxItemHandled("handled", now); err != nil {
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

func TestGardenMailboxItemsCoalesceUntilRead(t *testing.T) {
	s := newAgentMailboxStore(t)
	now := time.Now()
	first, claimed, err := s.ClaimGardenSeedMailboxItem("watcher", "s-7k3f9m", "note", "bell-1", now)
	if err != nil || !claimed || first.Item.Kind != agentmailbox.KindGardenSeed {
		t.Fatalf("first claim = %+v, %v, %v", first, claimed, err)
	}
	if _, claimed, err := s.ClaimGardenSeedMailboxItem("watcher", "s-7k3f9m", "harvested", "bell-2", now.Add(time.Second)); err != nil || claimed {
		t.Fatalf("coalesced claim = %v, %v", claimed, err)
	}
	if read, err := s.ReadGardenSeedMailboxItems("watcher", "s-7k3f9m", now.Add(2*time.Second)); err != nil || !read {
		t.Fatalf("read = %v, %v", read, err)
	}
	if _, claimed, err := s.ClaimGardenSeedMailboxItem("watcher", "s-7k3f9m", "harvested", "bell-2", now.Add(3*time.Second)); err != nil || !claimed {
		t.Fatalf("claim after read = %v, %v", claimed, err)
	}
}

func TestMigration129SeparatesMailboxReceiptsAndPayloads(t *testing.T) {
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
		DELETE FROM schema_migrations WHERE version >= 129;
	`); err != nil {
		t.Fatalf("plant pre-129 schema: %v", err)
	}

	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}

	queued, err := s.QueuedAgentMailboxDeliveries("target")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 3 {
		t.Fatalf("queued deliveries = %+v, want peer, maintenance and Garden", queued)
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
