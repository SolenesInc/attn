package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/analytics"
)

const analyticsTimeLayout = "2006-01-02T15:04:05.000000000Z"

type AnalyticsRow struct {
	ID     int64
	Record string
}

type AnalyticsPage struct {
	Rows    []AnalyticsRow
	Waiting int
}

func (s *Store) AppendAnalyticsRecord(record analytics.Record) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return fmt.Errorf("the analytics outbox needs the SQLite store")
	}
	_, err = s.db.Exec("INSERT INTO analytics_outbox (stream, seq, at, record) VALUES (?, ?, ?, ?) ON CONFLICT(stream, seq) DO NOTHING",
		record.Stream, record.Seq, record.At.UTC().Format(analyticsTimeLayout), string(encoded))
	return err
}

func (s *Store) AnalyticsRecords(stream analytics.Stream, beforeID int64, limit int) (AnalyticsPage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return AnalyticsPage{}, nil
	}
	query := "SELECT id, record FROM analytics_outbox WHERE 1 = 1"
	var args []any
	if stream != "" {
		query += " AND stream = ?"
		args = append(args, stream)
	}
	if beforeID > 0 {
		query += " AND id < ?"
		args = append(args, beforeID)
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return AnalyticsPage{}, err
	}
	defer rows.Close()
	page := AnalyticsPage{Rows: []AnalyticsRow{}}
	for rows.Next() {
		var row AnalyticsRow
		if err := rows.Scan(&row.ID, &row.Record); err != nil {
			return AnalyticsPage{}, err
		}
		page.Rows = append(page.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return AnalyticsPage{}, err
	}
	if err := s.db.QueryRow("SELECT count(*) FROM analytics_outbox WHERE sent_at IS NULL").Scan(&page.Waiting); err != nil {
		return AnalyticsPage{}, err
	}
	return page, nil
}

func (s *Store) PruneAnalyticsOutbox(before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return 0, nil
	}
	result, err := s.db.Exec("DELETE FROM analytics_outbox WHERE at < ?", before.UTC().Format(analyticsTimeLayout))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
