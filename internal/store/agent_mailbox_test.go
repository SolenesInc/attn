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
	if len(queued) != 1 || queued[0].Item.ID != "first" {
		t.Fatalf("queue exposed more than the oldest unread peer: %+v", queued)
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

	if err := s.MarkAgentMailboxItemNotified("first", now); err != nil {
		t.Fatal(err)
	}
	queued, err = s.QueuedAgentMailboxDeliveries("target")
	if err != nil || len(queued) != 0 {
		t.Fatalf("a second peer passed the notified unread message: %+v, %v", queued, err)
	}
	if _, changed, err := s.ReadPeerMessage("first", "target", now.Add(time.Second)); err != nil || !changed {
		t.Fatalf("read first = %v, %v", changed, err)
	}
	queued, err = s.QueuedAgentMailboxDeliveries("target")
	if err != nil || len(queued) != 1 || queued[0].Item.ID != "second" {
		t.Fatalf("after reading first, queue = %+v, %v", queued, err)
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

	queued, err := s.QueuedAgentMailboxDeliveries("target")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 || queued[0].Item.ID != "earlier" {
		t.Fatalf("oldest unread peer = %+v, want earlier", queued)
	}
	want := base.Format(sortableTimeFormat)
	if queued[0].Item.CreatedAt != want || queued[0].Peer == nil || queued[0].Peer.CreatedAt != want {
		t.Fatalf("created_at = %q/%v, want %q", queued[0].Item.CreatedAt, queued[0].Peer, want)
	}
}

func TestAgentMailboxDoesNotPassAnAlreadyNotifiedNewerPeer(t *testing.T) {
	s := newAgentMailboxStore(t)
	now := time.Now()
	enqueuePeer(t, s, "newer", "sender", "target", "landed first", now)
	if err := s.MarkAgentMailboxItemNotified("newer", now); err != nil {
		t.Fatal(err)
	}
	enqueuePeer(t, s, "older", "sender", "target", "persisted late", now.Add(-time.Second))

	queued, err := s.QueuedAgentMailboxDeliveries("target")
	if err != nil || len(queued) != 0 {
		t.Fatalf("queued behind a notified peer = %+v, %v", queued, err)
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
	if err := s.MarkAgentMailboxItemNotified("message", now); err != nil {
		t.Fatal(err)
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
	enqueuePeer(t, s, "handled", "sender", "target", "already read", now.Add(time.Second))
	if err := s.MarkAgentMailboxItemNotified("handled", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := s.ReadPeerMessage("handled", "target", now.Add(2*time.Second)); err != nil || !changed {
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
