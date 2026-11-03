package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

var ErrPairedSenderNameTaken = errors.New("a paired sender already has this name")

type PairedSender struct {
	protocol.PairedSender
	ProfileID string
}

func (s *Store) PairedSenders(profileID string) ([]protocol.PairedSender, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT id, name, created_at, last_used_at FROM paired_senders WHERE profile_id = ? ORDER BY created_at, id`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	senders := []protocol.PairedSender{}
	for rows.Next() {
		var p protocol.PairedSender
		var used string
		if err := rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &used); err != nil {
			return nil, err
		}
		if used != "" {
			p.LastUsedAt = protocol.Ptr(used)
		}
		senders = append(senders, p)
	}
	return senders, rows.Err()
}

func (s *Store) AddPairedSender(sender PairedSender, tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO paired_senders(id, profile_id, name, token_hash, created_at) VALUES(?,?,?,?,?)`, sender.ID, sender.ProfileID, sender.Name, tokenHash, sender.CreatedAt)
	if err != nil && strings.Contains(err.Error(), "paired_senders.name") {
		return fmt.Errorf("%w: %q", ErrPairedSenderNameTaken, sender.Name)
	}
	return err
}

func (s *Store) PairedSenderNameTaken(profileID, name string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var id string
	err := s.db.QueryRow(`SELECT id FROM paired_senders WHERE profile_id = ? AND name = ?`, profileID, name).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) UsePairedSender(tokenHash string, at time.Time) (*PairedSender, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var p PairedSender
	err := s.db.QueryRow(`SELECT id, profile_id, name, created_at FROM paired_senders WHERE token_hash = ?`, tokenHash).Scan(&p.ID, &p.ProfileID, &p.Name, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	used := at.UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(`UPDATE paired_senders SET last_used_at = ? WHERE id = ?`, used, p.ID); err != nil {
		return nil, err
	}
	p.LastUsedAt = protocol.Ptr(used)
	return &p, nil
}

func (s *Store) RemovePairedSender(profileID, idOrName string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`DELETE FROM paired_senders WHERE profile_id = ? AND (id = ? OR name = ?)`, profileID, idOrName, idOrName)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}
