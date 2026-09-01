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

func (s *Store) ClaimGardenSeedMailboxItem(watcherSessionID, seedID, eventKind, itemID string, now time.Time) (agentmailbox.Delivery, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := agentmailbox.Item{
		ID: itemID, RecipientSessionID: watcherSessionID,
		Kind: agentmailbox.KindGardenSeed, SourceID: seedID, CoalesceKey: seedID,
		Hint: eventKind, CreatedAt: now.UTC().Format(sortableTimeFormat),
	}
	tx, err := s.db.Begin()
	if err != nil {
		return agentmailbox.Delivery{}, false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`
		INSERT OR IGNORE INTO agent_mailbox_items
			(id, recipient_session_id, kind, source_id, coalesce_key, hint, prompt, created_at, notified_at, read_at)
		VALUES (?, ?, ?, ?, ?, ?, '', ?, '', '')
	`, item.ID, item.RecipientSessionID, item.Kind, item.SourceID, item.CoalesceKey, item.Hint, item.CreatedAt)
	if err != nil {
		return agentmailbox.Delivery{}, false, fmt.Errorf("claim Garden seed mailbox item: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return agentmailbox.Delivery{}, false, err
	}
	if n == 0 {
		return agentmailbox.Delivery{}, false, nil
	}
	if err := tx.Commit(); err != nil {
		return agentmailbox.Delivery{}, false, err
	}
	return agentmailbox.Delivery{Item: item}, true, nil
}
