package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type BusEvent struct {
	Seq       int64
	Name      string
	Subject   string
	Payload   string
	Source    string
	CreatedAt time.Time
}

type BusConsumer struct {
	Name          string
	Cursor        int64
	Filter        string
	Enabled       bool
	PinsRetention bool
	UpdatedAt     time.Time
}

func (s *Store) AppendBusEvent(e BusEvent, now time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return 0, nil
	}
	return appendBusEventWith(s.db, e, now)
}

func (s *Store) AppendBusEventOnce(
	sourceKind, sourceID string, event BusEvent, now time.Time,
) (int64, bool, error) {
	return s.AppendBusEventOnceReplacingSource(sourceKind, sourceID, "", event, now)
}

func (s *Store) AppendBusEventOnceReplacingSource(
	sourceKind, sourceID, replacedSourceID string, event BusEvent, now time.Time,
) (int64, bool, error) {
	if strings.TrimSpace(sourceKind) == "" || strings.TrimSpace(sourceID) == "" || strings.TrimSpace(event.Name) == "" {
		return 0, false, fmt.Errorf("append bus event once: source kind, source id, and event name are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return 0, false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()
	seq, inserted, err := appendBusEventOnceReplacingSourceWith(tx, sourceKind, sourceID, replacedSourceID, event, now)
	if err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return seq, inserted, nil
}

func appendBusEventOnceReplacingSourceWith(
	tx *sql.Tx, sourceKind, sourceID, replacedSourceID string, event BusEvent, now time.Time,
) (int64, bool, error) {
	var existing int64
	err := tx.QueryRow(`
		SELECT event_seq FROM garden_seed_event_sources
		WHERE source_kind=? AND source_id=? AND event_name=?
	`, sourceKind, sourceID, event.Name).Scan(&existing)
	if err == nil {
		if err := deleteReplacedBusEventSource(tx, sourceKind, sourceID, replacedSourceID); err != nil {
			return 0, false, err
		}
		return existing, false, nil
	}
	if err != sql.ErrNoRows {
		return 0, false, err
	}
	seq, err := appendBusEventWith(tx, event, now)
	if err != nil {
		return 0, false, err
	}
	if _, err := tx.Exec(`
		INSERT INTO garden_seed_event_sources(source_kind, source_id, event_name, event_seq)
		VALUES (?, ?, ?, ?)
	`, sourceKind, sourceID, event.Name, seq); err != nil {
		return 0, false, err
	}
	if err := deleteReplacedBusEventSource(tx, sourceKind, sourceID, replacedSourceID); err != nil {
		return 0, false, err
	}
	return seq, true, nil
}

func deleteReplacedBusEventSource(tx *sql.Tx, sourceKind, sourceID, replacedSourceID string) error {
	replacedSourceID = strings.TrimSpace(replacedSourceID)
	if replacedSourceID == "" || replacedSourceID == sourceID {
		return nil
	}
	_, err := tx.Exec(`DELETE FROM garden_seed_event_sources WHERE source_kind=? AND source_id=?`, sourceKind, replacedSourceID)
	return err
}

func (s *Store) AppendGardenSeedArtifactObservation(
	checksum string, event BusEvent, now time.Time,
) (int64, bool, error) {
	if strings.TrimSpace(checksum) == "" || strings.TrimSpace(event.Name) == "" || strings.TrimSpace(event.Subject) == "" {
		return 0, false, fmt.Errorf("append Garden seed artifact observation: checksum, event name, and subject are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return 0, false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var previousChecksum string
	var previousSeq int64
	err = tx.QueryRow(`
		SELECT checksum, event_seq FROM garden_seed_artifact_observations WHERE seed_id=?
	`, event.Subject).Scan(&previousChecksum, &previousSeq)
	switch err {
	case nil:
		if previousChecksum == checksum {
			return previousSeq, false, tx.Commit()
		}
	case sql.ErrNoRows:
	default:
		return 0, false, err
	}
	seq, err := appendBusEventWith(tx, event, now)
	if err != nil {
		return 0, false, err
	}
	if err := upsertGardenSeedArtifactObservation(tx, event.Subject, checksum, seq, now); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return seq, true, nil
}

func (s *Store) AppendGardenSeedArtifactTransferObservation(
	sourceID, replacedSourceID, checksum string, event BusEvent, now time.Time,
) (int64, bool, error) {
	if strings.TrimSpace(sourceID) == "" || strings.TrimSpace(checksum) == "" ||
		strings.TrimSpace(event.Name) == "" || strings.TrimSpace(event.Subject) == "" {
		return 0, false, fmt.Errorf("append Garden seed artifact transfer observation: source id, checksum, event name, and subject are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return 0, false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()
	seq, inserted, err := appendBusEventOnceReplacingSourceWith(
		tx, "artifact_transfer", sourceID, replacedSourceID, event, now,
	)
	if err != nil {
		return 0, false, err
	}
	if err := upsertGardenSeedArtifactObservation(tx, event.Subject, checksum, seq, now); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return seq, inserted, nil
}

func upsertGardenSeedArtifactObservation(tx *sql.Tx, seedID, checksum string, eventSeq int64, now time.Time) error {
	_, err := tx.Exec(`
		INSERT INTO garden_seed_artifact_observations(seed_id, checksum, event_seq, observed_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(seed_id) DO UPDATE SET
			checksum=excluded.checksum,
			event_seq=excluded.event_seq,
			observed_at=excluded.observed_at
	`, seedID, checksum, eventSeq, formatTicketTime(now))
	return err
}

func (s *Store) HasGardenSeedArtifactObservation(seedID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return false, nil
	}
	var present bool
	err := s.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM garden_seed_artifact_observations WHERE seed_id=?)
	`, seedID).Scan(&present)
	return present, err
}

func appendBusEventWith(x execer, e BusEvent, now time.Time) (int64, error) {
	res, err := x.Exec(`
		INSERT INTO bus_events (name, subject, payload, source, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, e.Name, e.Subject, e.Payload, e.Source, formatTicketTime(now))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) BusEventsSince(cursor int64, limit int) ([]BusEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT seq, name, subject, payload, source, created_at
		FROM bus_events WHERE seq > ? ORDER BY seq ASC LIMIT ?
	`, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []BusEvent
	for rows.Next() {
		var (
			e         BusEvent
			createdAt string
		)
		if err := rows.Scan(&e.Seq, &e.Name, &e.Subject, &e.Payload, &e.Source, &createdAt); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTicketTime(createdAt)
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) BusBounds() (earliest, head int64, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return 0, 0, nil
	}
	var lo, hi sql.NullInt64
	if err := s.db.QueryRow(`SELECT MIN(seq), MAX(seq) FROM bus_events`).Scan(&lo, &hi); err != nil {
		return 0, 0, err
	}
	return lo.Int64, hi.Int64, nil
}

func (s *Store) GetBusConsumer(name string) (BusConsumer, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return BusConsumer{}, false, nil
	}
	var (
		c         BusConsumer
		enabled   int
		updatedAt string
	)
	err := s.db.QueryRow(`
		SELECT name, cursor, filter, enabled, updated_at FROM bus_consumers WHERE name = ?
	`, name).Scan(&c.Name, &c.Cursor, &c.Filter, &enabled, &updatedAt)
	switch err {
	case nil:
		c.Enabled = enabled != 0
		c.UpdatedAt = parseTicketTime(updatedAt)
		return c, true, nil
	case sql.ErrNoRows:
		return BusConsumer{}, false, nil
	default:
		return BusConsumer{}, false, err
	}
}

// SaveBusConsumer creates or updates a registration. An existing row keeps its
// cursor and enabled bit: startup must not rewind or silently re-enable one.
func (s *Store) SaveBusConsumer(c BusConsumer, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO bus_consumers (name, cursor, filter, enabled, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET filter = excluded.filter, updated_at = excluded.updated_at
	`, c.Name, c.Cursor, c.Filter, enabled, formatTicketTime(now))
	return err
}

func (s *Store) SetBusConsumerCursor(name string, cursor int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`
		UPDATE bus_consumers SET cursor = ?, updated_at = ? WHERE name = ?
	`, cursor, formatTicketTime(now), name)
	return err
}

func (s *Store) AdvanceBusConsumerCursor(name string, cursor int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`
		UPDATE bus_consumers SET cursor = MAX(cursor, ?), updated_at = ? WHERE name = ?
	`, cursor, formatTicketTime(now), name)
	return err
}

func (s *Store) SetBusConsumerEnabled(name string, enabled bool, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return false, nil
	}
	flag := 0
	if enabled {
		flag = 1
	}
	res, err := s.db.Exec(`
		UPDATE bus_consumers SET enabled = ?, updated_at = ? WHERE name = ?
	`, flag, formatTicketTime(now), name)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) SetAppBusConsumerEnabled(appName string, enabled bool, now time.Time) (exists, changed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback() }()
	consumerName := "app:" + appName
	var current int
	if err := tx.QueryRow(`SELECT enabled FROM bus_consumers WHERE name = ?`, consumerName).Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			return false, false, nil
		}
		return false, false, err
	}
	want := 0
	if enabled {
		want = 1
	}
	if current == want {
		return true, false, tx.Commit()
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	if _, err := tx.Exec(`UPDATE bus_consumers SET enabled = ?, updated_at = ? WHERE name = ?`, want, stamp, consumerName); err != nil {
		return false, false, err
	}
	return true, true, tx.Commit()
}

// DeleteBusConsumer removes a registration; deleting a row that is not there is success.
// While an abandoned row exists and is enabled it holds the cursor floor down.
func (s *Store) DeleteBusConsumer(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM bus_consumers WHERE name = ?`, name)
	return err
}

func (s *Store) ListBusConsumers() ([]BusConsumer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT c.name, c.cursor, c.filter, c.enabled,
		       EXISTS (SELECT 1 FROM apps a WHERE c.name = 'app:' || a.name),
		       c.updated_at
		FROM bus_consumers c ORDER BY c.name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BusConsumer
	for rows.Next() {
		var (
			c         BusConsumer
			enabled   int
			updatedAt string
		)
		var pinsRetention int
		if err := rows.Scan(&c.Name, &c.Cursor, &c.Filter, &enabled, &pinsRetention, &updatedAt); err != nil {
			return nil, err
		}
		c.Enabled = enabled != 0
		c.PinsRetention = pinsRetention != 0
		c.UpdatedAt = parseTicketTime(updatedAt)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) TrimBusEvents(cutoff time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`
		DELETE FROM bus_events
		WHERE created_at < ?
		  AND seq <= COALESCE(
		      (SELECT MIN(c.cursor)
		         FROM bus_consumers c
		        WHERE c.enabled = 1
		           OR EXISTS (SELECT 1 FROM apps a WHERE c.name = 'app:' || a.name)),
		      (SELECT COALESCE(MAX(seq), 0) FROM bus_events)
		  )
	`, formatTicketTime(cutoff))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`
		DELETE FROM garden_seed_event_receipts
		WHERE NOT EXISTS (
			SELECT 1 FROM bus_events WHERE bus_events.seq = garden_seed_event_receipts.event_seq
		)
	`); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(n), nil
}

// CompactBusEvents keeps only the newest fact per subject among the named names, at or below
// the cursor floor. An empty name list compacts nothing, not everything.
func (s *Store) CompactBusEvents(names []string, floor int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil || len(names) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
	args := make([]any, 0, len(names)*2+1)
	for _, n := range names {
		args = append(args, n)
	}
	args = append(args, floor)
	for _, n := range names {
		args = append(args, n)
	}
	res, err := s.db.Exec(`
		DELETE FROM bus_events
		WHERE name IN (`+placeholders+`)
		  AND seq <= ?
		  AND seq < (
		      SELECT MAX(newer.seq) FROM bus_events AS newer
		      WHERE newer.subject = bus_events.subject
		        AND newer.name IN (`+placeholders+`)
		  )
	`, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

type BusProducer struct {
	Name     string
	Events   int64
	Bytes    int64
	Subjects int64
	Recent   []int64
}

// BusProducers reports every fact class with its totals and per-cutoff counts, loudest first.
// Measured on a copy of production: 209ms at 945k rows, which is why nothing polls it.
func (s *Store) BusProducers(cutoffs []time.Time) ([]BusProducer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	var (
		columns strings.Builder
		args    []any
	)
	for _, c := range cutoffs {
		columns.WriteString(", COALESCE(SUM(created_at >= ?), 0)")
		args = append(args, formatTicketTime(c))
	}
	rows, err := s.db.Query(`
		SELECT name,
		       COUNT(*),
		       COALESCE(SUM(LENGTH(name) + LENGTH(subject) + LENGTH(payload) + LENGTH(source) + LENGTH(created_at)), 0),
		       COUNT(DISTINCT subject)`+columns.String()+`
		FROM bus_events
		GROUP BY name
		ORDER BY COUNT(*) DESC, name ASC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BusProducer
	for rows.Next() {
		p := BusProducer{Recent: make([]int64, len(cutoffs))}
		dest := []any{&p.Name, &p.Events, &p.Bytes, &p.Subjects}
		for i := range p.Recent {
			dest = append(dest, &p.Recent[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) BusPendingBytes(above int64) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return 0, nil
	}
	var bytes int64
	err := s.db.QueryRow(`
		SELECT COALESCE(SUM(LENGTH(name) + LENGTH(subject) + LENGTH(payload) + LENGTH(source) + LENGTH(created_at)), 0)
		FROM bus_events WHERE seq > ?
	`, above).Scan(&bytes)
	if err != nil {
		return 0, err
	}
	return bytes, nil
}

func (s *Store) BusEventTimeAt(seq int64) (time.Time, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return time.Time{}, false, nil
	}
	var createdAt string
	err := s.db.QueryRow(`
		SELECT created_at FROM bus_events WHERE seq >= ? ORDER BY seq ASC LIMIT 1
	`, seq).Scan(&createdAt)
	switch err {
	case nil:
		return parseTicketTime(createdAt), true, nil
	case sql.ErrNoRows:
		return time.Time{}, false, nil
	default:
		return time.Time{}, false, err
	}
}

func (s *Store) BusLogSize() (rows int64, bytes int64, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return 0, 0, nil
	}
	err = s.db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(LENGTH(name) + LENGTH(subject) + LENGTH(payload) + LENGTH(source) + LENGTH(created_at)), 0)
		FROM bus_events
	`).Scan(&rows, &bytes)
	if err != nil {
		return 0, 0, err
	}
	return rows, bytes, nil
}
