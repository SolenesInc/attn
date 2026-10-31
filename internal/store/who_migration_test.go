package store

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

func TestPeerMessageSenderMigrationNamesTheMemberOrTheSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.db")
	old, err := newStoreAtVersion(path, 1793415974004927-1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`
INSERT INTO sessions(id,user_label,directory,created_at,member_key,closed_at) VALUES
 ('day','Keel','/tmp/peer','','keel','2026-10-01T00:00:00Z'),
 ('plain','Plain','/tmp/peer','','','');
INSERT INTO peer_messages(id,sender_session_id,body,created_at) VALUES
 ('from-day','day','from the member','2026-10-01T00:00:00Z'),
 ('from-plain','plain','from a session','2026-10-01T00:00:01Z'),
 ('from-gone','gone','from a forgotten session','2026-10-01T00:00:02Z');
INSERT INTO inbox_items(id,address,kind,source_id,created_at) VALUES
 ('from-day','session:reader','peer_message','from-day','2026-10-01T00:00:00Z'),
 ('from-plain','session:reader','peer_message','from-plain','2026-10-01T00:00:01Z'),
 ('from-gone','session:reader','peer_message','from-gone','2026-10-01T00:00:02Z');
`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := newStoreAtVersion(path, 1793415974004927)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	deliveries, err := upgraded.UnreadInboxDeliveries(who.ToSession("reader"))
	if err != nil {
		t.Fatal(err)
	}
	keel, err := who.ParseMemberKey("keel")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]who.Party{"from-day": who.Member(keel), "from-plain": who.Session("plain"), "from-gone": who.Session("gone")}
	if len(deliveries) != len(want) {
		t.Fatalf("deliveries: %+v", deliveries)
	}
	for _, d := range deliveries {
		if d.Peer == nil || d.Peer.Sender != want[d.Item.ID] {
			t.Errorf("%s sender: %+v, want %v", d.Item.ID, d.Peer, want[d.Item.ID])
		}
	}
}

func TestClosedByMigrationKeepsWhoClosedEachSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "closed.db")
	old, err := newStoreAtVersion(path, 1793416602286212-1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`
INSERT INTO sessions(id,user_label,directory,created_at,closed_at,closed_by) VALUES
 ('closer','Closer','/tmp/closed','','',''),
 ('by-agent','By agent','/tmp/closed','','2026-10-01T00:00:00Z','closer'),
 ('by-user','By user','/tmp/closed','','2026-10-01T00:00:00Z','user');
`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := newStoreAtVersion(path, 1793416602286212)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	for id, want := range map[protocol.SessionID]string{"by-agent": "closer", "by-user": "user"} {
		entry := upgraded.SessionLedgerEntry(id)
		if entry == nil || protocol.Deref(entry.ClosedBy) != want {
			t.Errorf("%s closed by %+v, want %q", id, entry, want)
		}
	}
	var stored string
	if err := upgraded.db.QueryRow("SELECT closed_by FROM sessions WHERE id = 'by-agent'").Scan(&stored); err != nil || stored != "session:closer" {
		t.Fatalf("stored actor %q, %v", stored, err)
	}
}
