package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/inbox"
)

func (s *Store) SeedReminder(seedID string) (*garden.Reminder, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var stored string
	err := s.db.QueryRow(`SELECT reminder FROM seed_reminders WHERE seed_id = ?`, seedID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var reminder garden.Reminder
	if err := json.Unmarshal([]byte(stored), &reminder); err != nil {
		return nil, err
	}
	return &reminder, nil
}

func (s *Store) SeedReminders() (map[string]garden.Reminder, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT seed_id, reminder FROM seed_reminders`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reminders := map[string]garden.Reminder{}
	for rows.Next() {
		var seedID, stored string
		if err := rows.Scan(&seedID, &stored); err != nil {
			return nil, err
		}
		var reminder garden.Reminder
		if err := json.Unmarshal([]byte(stored), &reminder); err != nil {
			return nil, err
		}
		reminders[seedID] = reminder
	}
	return reminders, rows.Err()
}

func (s *Store) DueSeedReminders(now time.Time) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT seed_id FROM seed_reminders WHERE remind_at <= ? ORDER BY seed_id`, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var due []string
	for rows.Next() {
		var seedID string
		if err := rows.Scan(&seedID); err != nil {
			return nil, err
		}
		due = append(due, seedID)
	}
	return due, rows.Err()
}

func (s *Store) DisarmSeedReminder(seedID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM seed_reminders WHERE seed_id = ?`, seedID)
	return err
}

func armSeedReminder(tx *sql.Tx, seedID string, reminder garden.Reminder) error {
	stored, err := json.Marshal(reminder)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO seed_reminders (seed_id, remind_at, reminder) VALUES (?, ?, ?)
		ON CONFLICT(seed_id) DO UPDATE SET remind_at = excluded.remind_at, reminder = excluded.reminder`,
		seedID, reminder.At.Unix(), string(stored))
	return err
}

type SeedChangeEffects struct {
	SeedID   string
	Post     *inbox.Item
	Withdraw *InboxKey
	Arm      *garden.Reminder
	Disarm   bool
}

func (s *Store) CommitSeedChange(
	commits []DocumentCommit, events []BusEvent, effects SeedChangeEffects, now time.Time,
) ([]DocumentWriteResult, []int64, error) {
	return s.commitDocumentWrites(commits, events, now, false, func(tx *sql.Tx) (bool, error) {
		if effects.Post != nil {
			if err := putInbox(tx, *effects.Post, now); err != nil {
				return false, err
			}
		}
		if effects.Withdraw != nil {
			if err := withdrawInbox(tx, *effects.Withdraw); err != nil {
				return false, err
			}
		}
		if effects.Disarm {
			if _, err := tx.Exec(`DELETE FROM seed_reminders WHERE seed_id = ?`, effects.SeedID); err != nil {
				return false, err
			}
		}
		if effects.Arm != nil {
			if err := armSeedReminder(tx, effects.SeedID, *effects.Arm); err != nil {
				return false, err
			}
		}
		return true, nil
	})
}
