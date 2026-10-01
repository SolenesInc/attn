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
}

const keptConversationColumns = "resume_id, agent, source_path, bytes, stored_bytes, copied_at, released_at, deleted_at"

func scanKeptConversation(row interface{ Scan(...any) error }) (KeptConversation, error) {
	var k KeptConversation
	var copied, released, deleted string
	err := row.Scan(&k.ResumeID, &k.Agent, &k.SourcePath, &k.Bytes, &k.StoredBytes, &copied, &released, &deleted)
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
	_, err := s.db.Exec(`INSERT INTO kept_conversations (`+keptConversationColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
 ON CONFLICT(agent, resume_id) DO UPDATE SET source_path=excluded.source_path, bytes=excluded.bytes, stored_bytes=excluded.stored_bytes,
 copied_at=excluded.copied_at, released_at=excluded.released_at, deleted_at=excluded.deleted_at`,
		k.ResumeID, k.Agent, k.SourcePath, k.Bytes, k.StoredBytes, keptInstant(k.CopiedAt), keptInstant(k.ReleasedAt), keptInstant(k.DeletedAt))
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
func (s *Store) TombstoneKeptConversation(agent, resumeID string, at time.Time) bool {
	return s.updateKeptConversation("UPDATE kept_conversations SET deleted_at=? WHERE agent=? AND resume_id=? AND deleted_at=''", agent, resumeID, keptInstant(at))
}
