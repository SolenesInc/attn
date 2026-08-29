package store

import (
	"database/sql"
	"time"
)

type TicketEventKind string

const (
	TicketEventCreated           TicketEventKind = "created"
	TicketEventStatusChanged     TicketEventKind = "status_changed"
	TicketEventCommented         TicketEventKind = "commented"
	TicketEventAssigned          TicketEventKind = "assigned"
	TicketEventDescriptionEdited TicketEventKind = "description_edited"
	TicketEventAttachmentAdded   TicketEventKind = "attachment_added"
	TicketEventAttachSubmitted   TicketEventKind = "attach_submitted"
)

type TicketEvent struct {
	Seq        int64
	TicketID   string
	Kind       TicketEventKind
	Author     string
	AuthorRole string
	FromStatus TicketStatus
	ToStatus   TicketStatus
	Comment    string
	Detail     string
	CreatedAt  time.Time
}

func (e TicketEvent) signature() string {
	return string(e.Kind) + "\x00" + string(e.FromStatus) + "\x00" + string(e.ToStatus) +
		"\x00" + e.Comment + "\x00" + e.Detail + "\x00" + e.Author
}

func (s *Store) AppendTicketEvent(e TicketEvent, now time.Time) (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return 0, false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()

	seq, appended, err := appendTicketEventTx(tx, e, now)
	if err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return seq, appended, nil
}

func appendTicketEventTx(tx *sql.Tx, e TicketEvent, now time.Time) (int64, bool, error) {
	var (
		lastSeq                                    int64
		lk, lfrom, lto, lcomment, ldetail, lauthor string
	)
	err := tx.QueryRow(`
		SELECT seq, kind, from_status, to_status, comment, detail, author
		FROM ticket_events WHERE ticket_id = ? ORDER BY seq DESC LIMIT 1
	`, e.TicketID).Scan(&lastSeq, &lk, &lfrom, &lto, &lcomment, &ldetail, &lauthor)
	switch err {
	case nil:
		prev := TicketEvent{
			Kind:       TicketEventKind(lk),
			FromStatus: TicketStatus(lfrom),
			ToStatus:   TicketStatus(lto),
			Comment:    lcomment,
			Detail:     ldetail,
			Author:     lauthor,
		}
		if prev.signature() == e.signature() {
			return lastSeq, false, nil
		}
	case sql.ErrNoRows:
	default:
		return 0, false, err
	}

	res, err := tx.Exec(`
		INSERT INTO ticket_events (ticket_id, kind, author, author_role, from_status, to_status, comment, detail, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, e.TicketID, string(e.Kind), e.Author, e.AuthorRole, string(e.FromStatus), string(e.ToStatus), e.Comment, e.Detail, formatTicketTime(now))
	if err != nil {
		return 0, false, err
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	return seq, true, nil
}

func scanTicketEventRows(rows *sql.Rows) ([]TicketEvent, error) {
	defer rows.Close()
	var events []TicketEvent
	for rows.Next() {
		var (
			e         TicketEvent
			kind      string
			from, to  string
			createdAt string
		)
		if err := rows.Scan(&e.Seq, &e.TicketID, &kind, &e.Author, &e.AuthorRole, &from, &to, &e.Comment, &e.Detail, &createdAt); err != nil {
			return nil, err
		}
		e.Kind = TicketEventKind(kind)
		e.FromStatus = TicketStatus(from)
		e.ToStatus = TicketStatus(to)
		e.CreatedAt = parseTicketTime(createdAt)
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) TicketEventsSince(cursor int64) ([]TicketEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT seq, ticket_id, kind, author, author_role, from_status, to_status, comment, detail, created_at
		FROM ticket_events WHERE seq > ? ORDER BY seq ASC
	`, cursor)
	if err != nil {
		return nil, err
	}
	return scanTicketEventRows(rows)
}

func (s *Store) UnreadTicketEvents(identity string) ([]TicketEvent, error) {
	return s.UnreadTicketEventsFor(identity, identity)
}

func (s *Store) UnreadTicketEventsFor(cursorIdentity, authorIdentity string) ([]TicketEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil || cursorIdentity == "" {
		return nil, nil
	}
	// The participation rule has exactly one definition, the ticket_participants
	// view; every query asks the view and never restates the rule.
	rows, err := s.db.Query(`
		SELECT e.seq, e.ticket_id, e.kind, e.author, e.author_role, e.from_status, e.to_status, e.comment, e.detail, e.created_at
		FROM ticket_events e
		LEFT JOIN ticket_event_cursors c
			ON c.identity = ? AND c.ticket_id = e.ticket_id
		WHERE e.author != ?
			AND e.seq > COALESCE(c.cursor, 0)
			AND e.ticket_id IN (
				SELECT ticket_id FROM ticket_participants WHERE identity = ?
			)
		ORDER BY e.ticket_id, e.seq ASC
	`, cursorIdentity, authorIdentity, cursorIdentity)
	if err != nil {
		return nil, err
	}
	return scanTicketEventRows(rows)
}

func (s *Store) TicketParticipants(ticketID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil || ticketID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT identity FROM ticket_participants WHERE ticket_id = ? ORDER BY 1 ASC`,
		ticketID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) LatestTicketEventSeq() (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return 0, nil
	}
	var seq sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(seq) FROM ticket_events`).Scan(&seq); err != nil {
		return 0, err
	}
	if !seq.Valid {
		return 0, nil
	}
	return seq.Int64, nil
}

func (s *Store) GetTicketCursor(identity, ticketID string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return 0, nil
	}
	var cursor int64
	err := s.db.QueryRow(`SELECT cursor FROM ticket_event_cursors WHERE identity = ? AND ticket_id = ?`, identity, ticketID).Scan(&cursor)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return cursor, nil
}

func (s *Store) SetTicketCursor(identity, ticketID string, cursor int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	return setTicketCursorTx(s.db, identity, ticketID, cursor, now)
}

type ticketExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// Only ever moves the cursor FORWARD: a stale write must never resurrect
// consumed events as unread.
func setTicketCursorTx(ex ticketExecer, identity, ticketID string, cursor int64, now time.Time) error {
	_, err := ex.Exec(`
		INSERT INTO ticket_event_cursors (identity, ticket_id, cursor, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(identity, ticket_id) DO UPDATE SET
			cursor = MAX(cursor, excluded.cursor),
			updated_at = excluded.updated_at
	`, identity, ticketID, cursor, formatTicketTime(now))
	return err
}
