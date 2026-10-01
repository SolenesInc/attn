package store

import (
	"database/sql"
	"log"
	"time"
)

type KeptConversation struct {
	ResumeID    string
	Agent       string
	SourcePath  string
	Bytes       int64
	StoredBytes int64
	CopiedAt    time.Time
	ReleasedAt  time.Time
	DeletedAt   time.Time
	DeletedBy   string
}

const keptConversationColumns = "resume_id, agent, source_path, bytes, stored_bytes, copied_at, released_at, deleted_at, deleted_by"

func scanKeptConversation(row interface{ Scan(...any) error }) (KeptConversation, error) {
	var k KeptConversation
	var copied, released, deleted string
	err := row.Scan(&k.ResumeID, &k.Agent, &k.SourcePath, &k.Bytes, &k.StoredBytes, &copied, &released, &deleted, &k.DeletedBy)
	k.CopiedAt, _ = time.Parse(time.RFC3339Nano, copied)
	k.ReleasedAt, _ = time.Parse(time.RFC3339Nano, released)
	k.DeletedAt, _ = time.Parse(time.RFC3339Nano, deleted)
	return k, err
}

func (s *Store) KeptConversation(agent, resumeID string) (KeptConversation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return KeptConversation{}, false
	}
	k, err := scanKeptConversation(s.db.QueryRow("SELECT "+keptConversationColumns+" FROM kept_conversations WHERE agent=? AND resume_id=?", agent, resumeID))
	if err != nil && err != sql.ErrNoRows {
		log.Printf("[store] kept conversation: %v", err)
	}
	return k, err == nil
}

func (s *Store) KeptConversations() ([]KeptConversation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, sql.ErrConnDone
	}
	rows, err := s.db.Query("SELECT " + keptConversationColumns + " FROM kept_conversations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []KeptConversation
	for rows.Next() {
		k, err := scanKeptConversation(rows)
		if err != nil {
			return nil, err
		}
		all = append(all, k)
	}
	return all, rows.Err()
}

func keptInstant(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func (s *Store) PutKeptConversation(k KeptConversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return sql.ErrConnDone
	}
	_, err := s.db.Exec(`INSERT INTO kept_conversations (`+keptConversationColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
 ON CONFLICT(agent, resume_id) DO UPDATE SET source_path=excluded.source_path, bytes=excluded.bytes, stored_bytes=excluded.stored_bytes,
 copied_at=excluded.copied_at, released_at=excluded.released_at, deleted_at=excluded.deleted_at, deleted_by=excluded.deleted_by`,
		k.ResumeID, k.Agent, k.SourcePath, k.Bytes, k.StoredBytes, keptInstant(k.CopiedAt), keptInstant(k.ReleasedAt), keptInstant(k.DeletedAt), k.DeletedBy)
	return err
}

func (s *Store) updateKeptConversation(query, agent, resumeID, at string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false
	}
	result, err := s.db.Exec(query, at, agent, resumeID)
	if err != nil {
		log.Printf("[store] kept conversation update: %v", err)
		return false
	}
	n, err := result.RowsAffected()
	return err == nil && n > 0
}

func (s *Store) ReleaseKeptConversation(agent, resumeID string, at time.Time) bool {
	return s.updateKeptConversation("UPDATE kept_conversations SET released_at=? WHERE agent=? AND resume_id=? AND released_at='' AND deleted_at=''", agent, resumeID, keptInstant(at))
}
func (s *Store) RetainKeptConversation(agent, resumeID string) bool {
	return s.updateKeptConversation("UPDATE kept_conversations SET released_at=? WHERE agent=? AND resume_id=? AND released_at!='' AND deleted_at=''", agent, resumeID, "")
}
func (s *Store) TombstoneKeptConversation(agent, resumeID string, at time.Time, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return sql.ErrConnDone
	}
	_, err := s.db.Exec("UPDATE kept_conversations SET deleted_at=?, deleted_by=? WHERE agent=? AND resume_id=? AND deleted_at=''", keptInstant(at), by, agent, resumeID)
	return err
}

type ConversationPin struct {
	Agent     string
	ResumeID  string
	SessionID string
	PinnedAt  time.Time
}

func (s *Store) PinConversation(agent, resumeID, sessionID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return sql.ErrConnDone
	}
	_, err := s.db.Exec(`INSERT INTO kept_conversation_pins (agent, resume_id, session_id, pinned_at) VALUES (?, ?, ?, ?)
 ON CONFLICT(agent, resume_id) DO UPDATE SET session_id=excluded.session_id`, agent, resumeID, sessionID, keptInstant(at))
	return err
}

func (s *Store) UnpinConversation(agent, resumeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return sql.ErrConnDone
	}
	_, err := s.db.Exec("DELETE FROM kept_conversation_pins WHERE agent=? AND resume_id=?", agent, resumeID)
	return err
}

func (s *Store) ConversationPins() ([]ConversationPin, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, sql.ErrConnDone
	}
	rows, err := s.db.Query("SELECT agent, resume_id, session_id, pinned_at FROM kept_conversation_pins")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pins []ConversationPin
	for rows.Next() {
		var pin ConversationPin
		var at string
		if err := rows.Scan(&pin.Agent, &pin.ResumeID, &pin.SessionID, &at); err != nil {
			return nil, err
		}
		pin.PinnedAt, _ = time.Parse(time.RFC3339Nano, at)
		pins = append(pins, pin)
	}
	return pins, rows.Err()
}

type ConversationSession struct {
	ID       string
	Label    string
	Agent    string
	ResumeID string
}

func (s *Store) ConversationSessions(identifier string) ([]ConversationSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, sql.ErrConnDone
	}
	query := `SELECT id, label, agent, resume_session_id FROM sessions WHERE `
	var args []any
	if identifier != "" {
		query += `(id=? OR resume_session_id=?)`
		args = []any{identifier, identifier}
	} else {
		query += `EXISTS (SELECT 1 FROM kept_conversations k WHERE k.agent=sessions.agent AND k.resume_id=sessions.resume_session_id) OR EXISTS (SELECT 1 FROM kept_conversation_pins p WHERE p.agent=sessions.agent AND p.resume_id=sessions.resume_session_id)`
	}
	rows, err := s.db.Query(query+" ORDER BY "+ledgerAt+" DESC, id DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []ConversationSession
	for rows.Next() {
		var entry ConversationSession
		if err := rows.Scan(&entry.ID, &entry.Label, &entry.Agent, &entry.ResumeID); err != nil {
			return nil, err
		}
		sessions = append(sessions, entry)
	}
	return sessions, rows.Err()
}
