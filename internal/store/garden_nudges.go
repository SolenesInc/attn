package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
)

type GardenSeedWatch struct {
	WatcherSessionID string
	SeedID           string
}

func (s *Store) SetGardenSeedWatch(watcherSessionID, seedID string, watching bool, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		res sql.Result
		err error
	)
	if watching {
		res, err = s.db.Exec(`
			INSERT OR IGNORE INTO garden_seed_watches(watcher_session_id, seed_id, created_at)
			VALUES (?, ?, ?)
		`, watcherSessionID, seedID, now.UTC().Format(sortableTimeFormat))
	} else {
		res, err = s.db.Exec(`DELETE FROM garden_seed_watches WHERE watcher_session_id = ? AND seed_id = ?`,
			watcherSessionID, seedID)
	}
	if err != nil {
		return false, fmt.Errorf("set garden seed watch: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) GardenSeedWatching(watcherSessionID, seedID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var watching bool
	if err := s.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM garden_seed_watches WHERE watcher_session_id = ? AND seed_id = ?)
	`, watcherSessionID, seedID).Scan(&watching); err != nil {
		return false, fmt.Errorf("read garden seed watch: %w", err)
	}
	return watching, nil
}

func (s *Store) GardenSeedWatches() ([]GardenSeedWatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`SELECT watcher_session_id, seed_id FROM garden_seed_watches`)
	if err != nil {
		return nil, fmt.Errorf("list garden seed watches: %w", err)
	}
	defer rows.Close()
	var watches []GardenSeedWatch
	for rows.Next() {
		var watch GardenSeedWatch
		if err := rows.Scan(&watch.WatcherSessionID, &watch.SeedID); err != nil {
			return nil, fmt.Errorf("scan garden seed watch: %w", err)
		}
		watches = append(watches, watch)
	}
	return watches, rows.Err()
}

func (s *Store) ClaimGardenSeedMailboxItem(watcherSessionID, seedID, eventKind, itemID string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	createdAt := now.UTC().Format(sortableTimeFormat)
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`
		INSERT OR IGNORE INTO agent_mailbox_items
			(id, recipient_session_id, kind, source_id, coalesce_key, hint, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, itemID, watcherSessionID, agentmailbox.KindGardenSeed, seedID, seedID, eventKind, createdAt)
	if err != nil {
		return false, fmt.Errorf("claim Garden seed mailbox item: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) UnreadGardenSeedMailboxSeeds(sessionID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT DISTINCT source_id FROM agent_mailbox_items
  WHERE recipient_session_id = ? AND kind = ? AND read_at = ''`, sessionID, agentmailbox.KindGardenSeed)
	if err != nil {
		return nil, fmt.Errorf("read queued Garden seeds: %w", err)
	}
	defer rows.Close()
	var seeds []string
	for rows.Next() {
		var seed string
		if err := rows.Scan(&seed); err != nil {
			return nil, err
		}
		seeds = append(seeds, seed)
	}
	return seeds, rows.Err()
}

func (s *Store) DiscardGardenSeedMailboxItems(sessionID string, seedIDs []string, now time.Time) error {
	if len(seedIDs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := now.UTC().Format(sortableTimeFormat)
	for _, seedID := range seedIDs {
		if _, err := tx.Exec(`UPDATE agent_mailbox_items
   SET read_at = ?, notified_at = CASE WHEN notified_at = '' THEN ? ELSE notified_at END
   WHERE recipient_session_id = ? AND kind = ? AND source_id = ? AND read_at = ''`,
			stamp, stamp, sessionID, agentmailbox.KindGardenSeed, seedID); err != nil {
			return fmt.Errorf("discard uncovered Garden update: %w", err)
		}
	}
	return tx.Commit()
}
