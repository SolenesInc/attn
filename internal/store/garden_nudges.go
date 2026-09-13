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

type GardenSeedMailboxItem struct {
	RecipientSessionID string
	SeedID             string
	BellName           string
}

type GardenSeedBellDelivery struct {
	RecipientSessionID string
	ItemID             string
}

const gardenSeedUnblockedHint = "unblocked"

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

func (s *Store) HandleGardenSeedEvent(
	eventSeq int64,
	seedID string,
	eventName string,
	bellName string,
	deliveries []GardenSeedBellDelivery,
	now time.Time,
) ([]string, bool, error) {
	if eventSeq <= 0 {
		return nil, false, fmt.Errorf("handle Garden seed event: event_seq must be positive, got %d", eventSeq)
	}
	if seedID == "" {
		return nil, false, fmt.Errorf("handle Garden seed event %d: seed id is required", eventSeq)
	}
	if eventName == "" {
		return nil, false, fmt.Errorf("handle Garden seed event %d: event name is required", eventSeq)
	}
	if bellName == "" && len(deliveries) != 0 {
		return nil, false, fmt.Errorf("handle Garden seed event %d: quiet event has %d deliveries", eventSeq, len(deliveries))
	}
	for _, delivery := range deliveries {
		if delivery.RecipientSessionID == "" || delivery.ItemID == "" {
			return nil, false, fmt.Errorf("handle Garden seed event %d: delivery recipient and item id are required", eventSeq)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	stamp := now.UTC().Format(sortableTimeFormat)
	res, err := tx.Exec(`
		INSERT OR IGNORE INTO garden_seed_event_receipts(event_seq, handled_at)
		VALUES (?, ?)
	`, eventSeq, stamp)
	if err != nil {
		return nil, false, fmt.Errorf("handle Garden seed event %d: write receipt: %w", eventSeq, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	if n == 0 {
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}

	created := make([]string, 0, len(deliveries))
	seen := make(map[string]bool, len(deliveries))
	for _, delivery := range deliveries {
		if seen[delivery.RecipientSessionID] {
			return nil, false, fmt.Errorf("handle Garden seed event %d: duplicate recipient %s", eventSeq, delivery.RecipientSessionID)
		}
		seen[delivery.RecipientSessionID] = true
		res, err := tx.Exec(`
			INSERT OR IGNORE INTO agent_mailbox_items
				(id, recipient_session_id, kind, source_id, coalesce_key, hint, bell_name, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, delivery.ItemID, delivery.RecipientSessionID, agentmailbox.KindGardenSeed,
			seedID, seedID, eventName, bellName, stamp)
		if err != nil {
			return nil, false, fmt.Errorf("handle Garden seed event %d: enqueue %s: %w", eventSeq, delivery.RecipientSessionID, err)
		}
		inserted, err := res.RowsAffected()
		if err != nil {
			return nil, false, err
		}
		if inserted == 1 {
			created = append(created, delivery.RecipientSessionID)
			continue
		}
		var existingID string
		if err := tx.QueryRow(`
			SELECT id FROM agent_mailbox_items
			WHERE recipient_session_id = ? AND kind = ? AND coalesce_key = ? AND read_at = ''
		`, delivery.RecipientSessionID, agentmailbox.KindGardenSeed, seedID).Scan(&existingID); err != nil {
			return nil, false, fmt.Errorf("handle Garden seed event %d: ignored item %s has no coalesced delivery: %w", eventSeq, delivery.ItemID, err)
		}
		if eventName == gardenSeedUnblockedHint {
			if _, err := tx.Exec(`UPDATE agent_mailbox_items SET hint = ? WHERE id = ?`, eventName, existingID); err != nil {
				return nil, false, fmt.Errorf("handle Garden seed event %d: promote coalesced delivery %s: %w", eventSeq, existingID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return created, true, nil
}

func (s *Store) UnreadGardenSeedMailboxItems(sessionID string) ([]GardenSeedMailboxItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT source_id, bell_name FROM agent_mailbox_items
  WHERE recipient_session_id = ? AND kind = ? AND read_at = ''`, sessionID, agentmailbox.KindGardenSeed)
	if err != nil {
		return nil, fmt.Errorf("read queued Garden seed mailbox items: %w", err)
	}
	defer rows.Close()
	var items []GardenSeedMailboxItem
	for rows.Next() {
		var item GardenSeedMailboxItem
		item.RecipientSessionID = sessionID
		if err := rows.Scan(&item.SeedID, &item.BellName); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) PendingGardenSeedMailboxItems() ([]GardenSeedMailboxItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT recipient_session_id, source_id, bell_name FROM agent_mailbox_items
  WHERE kind = ? AND read_at = '' ORDER BY recipient_session_id, source_id`, agentmailbox.KindGardenSeed)
	if err != nil {
		return nil, fmt.Errorf("read all queued Garden seed mailbox items: %w", err)
	}
	defer rows.Close()
	var items []GardenSeedMailboxItem
	for rows.Next() {
		var item GardenSeedMailboxItem
		if err := rows.Scan(&item.RecipientSessionID, &item.SeedID, &item.BellName); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) PendingGardenSeedBellNames() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT DISTINCT bell_name FROM agent_mailbox_items
	  WHERE kind = ? AND read_at = '' ORDER BY bell_name`, agentmailbox.KindGardenSeed)
	if err != nil {
		return nil, fmt.Errorf("read queued Garden seed bell definitions: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func (s *Store) UnreadGardenSeedMailboxSeeds(sessionID string) ([]string, error) {
	items, err := s.UnreadGardenSeedMailboxItems(sessionID)
	if err != nil {
		return nil, err
	}
	seeds := make([]string, 0, len(items))
	for _, item := range items {
		seeds = append(seeds, item.SeedID)
	}
	return seeds, nil
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
