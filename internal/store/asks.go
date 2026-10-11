package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
)

var ErrAskNotOpen = errors.New("the ask is not open")

const (
	AskAnswered      = "answered"
	AskWithdrawn     = "withdrawn"
	AskSessionClosed = "session_closed"
)

type Ask struct {
	ID        string
	SessionID protocol.SessionID
	To        inbox.Address
	Subject   string
	Markdown  string
	BaseDir   string
	CreatedAt string
}

const askColumns = "id, session_id, address, subject, markdown, base_dir, created_at"

func (s *Store) AddAsk(a Ask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return fmt.Errorf("asks need the SQLite store")
	}
	_, err := s.db.Exec("INSERT INTO asks ("+askColumns+") VALUES (?, ?, ?, ?, ?, ?, ?)",
		a.ID, a.SessionID, a.To.String(), a.Subject, a.Markdown, a.BaseDir, a.CreatedAt)
	return err
}

func (s *Store) OpenAsks() ([]Ask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query("SELECT " + askColumns + " FROM asks WHERE closed_at = '' ORDER BY created_at, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var asks []Ask
	for rows.Next() {
		a, err := scanAsk(rows)
		if err != nil {
			return nil, err
		}
		asks = append(asks, a)
	}
	return asks, rows.Err()
}

func (s *Store) GetOpenAsk(id string) (Ask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return Ask{}, ErrAskNotOpen
	}
	a, err := scanAsk(s.db.QueryRow("SELECT "+askColumns+" FROM asks WHERE id = ? AND closed_at = ''", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Ask{}, ErrAskNotOpen
	}
	return a, err
}

func (s *Store) CloseAsk(id, reason string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return ErrAskNotOpen
	}
	result, err := s.db.Exec("UPDATE asks SET closed_at = ?, close_reason = ? WHERE id = ? AND closed_at = ''", now.UTC().Format(time.RFC3339Nano), reason, id)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return ErrAskNotOpen
	}
	return nil
}

func (s *Store) CloseAsksTo(to inbox.Address, reason string, now time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query("UPDATE asks SET closed_at = ?, close_reason = ? WHERE address = ? AND closed_at = '' RETURNING id", now.UTC().Format(time.RFC3339Nano), reason, to.String())
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

func scanAsk(row interface{ Scan(...any) error }) (Ask, error) {
	var a Ask
	var address string
	if err := row.Scan(&a.ID, &a.SessionID, &address, &a.Subject, &a.Markdown, &a.BaseDir, &a.CreatedAt); err != nil {
		return Ask{}, err
	}
	to, err := inbox.ParseAddress(address)
	if err != nil {
		return Ask{}, err
	}
	a.To = to
	return a, nil
}
