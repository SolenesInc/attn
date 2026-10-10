package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

type GardenSeedWatch struct {
	WatcherSessionID protocol.SessionID
	SeedID           string
}

type GardenPartyWatch struct {
	Watcher who.Party
	SeedID  string
}

type GardenSeedMailboxItem struct {
	To       who.Address
	SeedID   string
	BellName string
}

type GardenSeedBellDelivery struct {
	To     who.Address
	ItemID string
}

const gardenSeedUnblockedHint = "unblocked"

func (s *Store) SetGardenSeedWatch(watcher who.Party, seedID string, watching bool, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		res sql.Result
		err error
	)
	if watching {
		res, err = s.db.Exec(`
			INSERT OR IGNORE INTO garden_seed_watches(watcher, seed_id, created_at)
			VALUES (?, ?, ?)
		`, watcher, seedID, now.UTC().Format(sortableTimeFormat))
	} else {
		res, err = s.db.Exec(`DELETE FROM garden_seed_watches WHERE watcher = ? AND seed_id = ?`,
			watcher, seedID)
	}
	if err != nil {
		return false, fmt.Errorf("set garden seed watch: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) GardenSeedWatches() ([]GardenPartyWatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`SELECT watcher, seed_id FROM garden_seed_watches`)
	if err != nil {
		return nil, fmt.Errorf("list garden seed watches: %w", err)
	}
	defer rows.Close()
	var watches []GardenPartyWatch
	for rows.Next() {
		var watch GardenPartyWatch
		if err := rows.Scan(&watch.Watcher, &watch.SeedID); err != nil {
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
		if delivery.To == (who.Address{}) || delivery.ItemID == "" {
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
		if seen[delivery.To.String()] {
			return nil, false, fmt.Errorf("handle Garden seed event %d: duplicate recipient %s", eventSeq, delivery.To.String())
		}
		seen[delivery.To.String()] = true
		var existingID string
		err := tx.QueryRow(`SELECT id FROM inbox_items WHERE address=? AND kind=? AND coalesce_key=? AND read_at=''`, delivery.To.String(), inbox.SeedUpdate, seedID).Scan(&existingID)
		if err == sql.ErrNoRows {
			if err := putInbox(tx, inbox.Item{ID: delivery.ItemID, To: delivery.To, Kind: inbox.SeedUpdate, Source: seedID, Key: seedID, Hint: eventName}, now); err != nil {
				return nil, false, err
			}
			if _, err := tx.Exec("UPDATE inbox_items SET bell_name=? WHERE id=?", bellName, delivery.ItemID); err != nil {
				return nil, false, err
			}
			created = append(created, delivery.To.String())
			continue
		}
		if err != nil {
			return nil, false, err
		}

		if eventName == gardenSeedUnblockedHint {
			if _, err := tx.Exec(`UPDATE inbox_items SET hint = ? WHERE id = ?`, eventName, existingID); err != nil {
				return nil, false, fmt.Errorf("handle Garden seed event %d: promote coalesced delivery %s: %w", eventSeq, existingID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return created, true, nil
}

func (s *Store) UnreadGardenSeedMailboxItems(to who.Address) ([]GardenSeedMailboxItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT source_id, bell_name FROM inbox_items
  WHERE address = ? AND kind = ? AND read_at = ''`, to.String(), inbox.SeedUpdate)
	if err != nil {
		return nil, fmt.Errorf("read queued Garden seed mailbox items: %w", err)
	}
	defer rows.Close()
	var items []GardenSeedMailboxItem
	for rows.Next() {
		var item GardenSeedMailboxItem
		item.To = to
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
	rows, err := s.db.Query(`SELECT address, source_id, bell_name FROM inbox_items
  WHERE kind = ? AND read_at = '' ORDER BY address, source_id`, inbox.SeedUpdate)
	if err != nil {
		return nil, fmt.Errorf("read all queued Garden seed mailbox items: %w", err)
	}
	defer rows.Close()
	var items []GardenSeedMailboxItem
	for rows.Next() {
		var item GardenSeedMailboxItem
		var address string
		if err := rows.Scan(&address, &item.SeedID, &item.BellName); err != nil {
			return nil, err
		}
		item.To, err = who.ParseAddress(address)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) PendingGardenSeedBellNames() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT DISTINCT bell_name FROM inbox_items
	  WHERE kind = ? AND read_at = '' ORDER BY bell_name`, inbox.SeedUpdate)
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

func (s *Store) UnreadGardenSeedMailboxSeeds(sessionID protocol.SessionID) ([]string, error) {
	items, err := s.UnreadGardenSeedMailboxItems(who.ToSession(sessionID))
	if err != nil {
		return nil, err
	}
	seeds := make([]string, 0, len(items))
	for _, item := range items {
		seeds = append(seeds, item.SeedID)
	}
	return seeds, nil
}
