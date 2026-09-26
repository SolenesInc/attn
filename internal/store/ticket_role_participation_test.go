package store

import (
	"path/filepath"
	"testing"
)

const migration82View = `
	DROP VIEW IF EXISTS ticket_participants;
	CREATE VIEW ticket_participants (ticket_id, identity) AS
		SELECT id, assignee FROM tickets WHERE assignee != ''
		UNION
		SELECT e.ticket_id, e.author FROM ticket_events e
		WHERE e.author != '' AND e.kind != 'commented'
			AND NOT (
				e.kind = 'created' AND EXISTS (
					SELECT 1 FROM ticket_role_owners ro WHERE ro.ticket_id = e.ticket_id
				)
			)
		UNION
		SELECT ticket_id, identity FROM ticket_subscriptions WHERE identity != ''
		UNION
		SELECT ticket_id, ('role:' || role) FROM ticket_role_owners WHERE role != '';
`

func participantSet(t *testing.T, s *Store, ticketID string) map[string]bool {
	t.Helper()
	identities, err := s.TicketParticipants(ticketID)
	if err != nil {
		t.Fatalf("TicketParticipants(%s): %v", ticketID, err)
	}
	set := map[string]bool{}
	for _, identity := range identities {
		set[identity] = true
	}
	return set
}

func TestMigration99DetachesPastChiefSessionsFromTheirDelegations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-99.db")
	db, err := openSeededDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB setup: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO tickets (id, title, description, status, assignee, cwd, last_agent_id, created_at, updated_at) VALUES
			('minted',  'Minted',  'Delegated from scratch.', 'working', 'agent-1', '/tmp/1', 'codex', '2026-08-01T10:00:00Z', '2026-08-01T10:00:00Z'),
			('adopted', 'Adopted', 'Filed, then delegated.',  'working', 'agent-2', '/tmp/2', 'codex', '2026-08-01T09:00:00Z', '2026-08-01T11:00:00Z'),
			('watched', 'Watched', 'Delegated, then re-followed by hand.', 'working', 'agent-3', '/tmp/3', 'codex', '2026-08-01T11:30:00Z', '2026-08-01T12:00:00Z');

		INSERT INTO ticket_events (ticket_id, kind, author, from_status, to_status, comment, detail, created_at) VALUES
			('minted',  'created',        'chief-a',  '', 'working', '', '',        '2026-08-01T10:00:00Z'),
			('adopted', 'created',        'author-x', '', 'todo',    '', '',        '2026-08-01T09:00:00Z'),
			('adopted', 'assigned',       'chief-a',  '', '',        '', 'agent-2', '2026-08-01T11:00:00Z'),
			('adopted', 'status_changed', 'chief-a',  'todo', 'working', '', '',    '2026-08-01T11:00:00Z'),
			('watched', 'created',        'chief-a',  '', 'working', '', '',        '2026-08-01T11:30:00Z');

		INSERT INTO ticket_role_owners (role, ticket_id, created_at) VALUES
			('chief_of_staff', 'minted',  '2026-08-01T10:00:00Z'),
			('chief_of_staff', 'adopted', '2026-08-01T11:00:00Z'),
			('chief_of_staff', 'watched', '2026-08-01T11:30:00Z');

		INSERT INTO ticket_subscriptions (identity, ticket_id, created_at) VALUES
			('chief-a', 'minted',  '2026-08-01T10:00:00Z'),
			('chief-a', 'adopted', '2026-08-01T11:00:00Z'),
			('chief-a', 'watched', '2026-08-01T12:00:00Z'),
			('watcher', 'minted',  '2026-08-01T10:30:00Z');

		INSERT INTO ticket_event_cursors (identity, ticket_id, cursor, updated_at) VALUES
			('role:chief_of_staff', 'minted', 1, '2026-08-01T10:05:00Z');

	` + migration82View + `
		ALTER TABLE ticket_events DROP COLUMN author_role;
		DELETE FROM schema_migrations WHERE version >= 99;
	`); err != nil {
		t.Fatalf("seed pre-99 database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close pre-99 database: %v", err)
	}

	migrated, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = migrated.Close() })

	role := TicketRoleIdentity(TicketRoleChiefOfStaff)
	for _, ticketID := range []string{"minted", "adopted"} {
		participants := participantSet(t, migrated, ticketID)
		if participants["chief-a"] {
			t.Errorf("%s: chief-a is still attached personally after the migration", ticketID)
		}
		if !participants[role] {
			t.Errorf("%s: the chief role lost its attachment", ticketID)
		}
	}

	minted := participantSet(t, migrated, "minted")
	if !minted["agent-1"] || !minted["watcher"] {
		t.Errorf("minted participants = %v, want the assignee and the watcher carried", minted)
	}
	adopted := participantSet(t, migrated, "adopted")
	if !adopted["agent-2"] || !adopted["author-x"] {
		t.Errorf("adopted participants = %v, want the assignee and the original author carried", adopted)
	}
	watching, err := migrated.IsTicketSubscribed("chief-a", "watched")
	if err != nil || !watching {
		t.Errorf("hand-made subscription survived = %v (err %v), want it carried", watching, err)
	}
	cursor, err := migrated.GetTicketCursor(role, "minted")
	if err != nil || cursor != 1 {
		t.Errorf("role cursor on minted = %d (err %v), want the pre-migration 1", cursor, err)
	}
}
