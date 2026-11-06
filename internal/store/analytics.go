package store

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/setting"
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

type UnsentAnalytics struct {
	Build      analytics.Build
	Rows       []AnalyticsRow
	Generation string
}

var ErrStaleAnalyticsGeneration = errors.New("these analytics records come from before this home last turned analytics off, cleared its record or forgot its install; the outpost drops them when it next hears from the home")

func (s *Store) homeSettingLocked(key, home string) string {
	if home == "" {
		return s.settings[key]
	}
	return s.settingCopies[home][key]
}

func (s *Store) analyticsGenerationLocked(home string) string {
	secret := s.homeSettingLocked(setting.AnalyticsSubjectSecret.Key, home)
	if secret == "" {
		return ""
	}
	return analytics.HashSubject([]byte(secret), "generation/"+s.homeSettingLocked(setting.AnalyticsClearedAt.Key, home))
}

func (s *Store) hashedWithCurrentSecretLocked(secret []byte, home string) bool {
	return s.homeSettingLocked(setting.AnalyticsSubjectSecret.Key, home) == hex.EncodeToString(secret)
}

func (s *Store) AppendAnalyticsRecord(record analytics.Record, build analytics.Build, secret []byte, home string) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return fmt.Errorf("the analytics outbox needs the SQLite store")
	}
	if !s.hashedWithCurrentSecretLocked(secret, home) {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	buildID, err := analyticsBuildID(tx, build)
	if err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO analytics_outbox (stream, seq, at, record, build_id) VALUES (?, ?, ?, ?, ?) ON CONFLICT(stream, seq) DO NOTHING",
		record.Stream, record.Seq, record.At.UTC().Format(analyticsTimeLayout), string(encoded), buildID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AppendAnalyticsStreamRecord(record analytics.Record, build analytics.Build, secret []byte, home string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return fmt.Errorf("the analytics outbox needs the SQLite store")
	}
	if !s.hashedWithCurrentSecretLocked(secret, home) {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := appendStreamRecord(tx, record, build); err != nil {
		return err
	}
	return tx.Commit()
}

func appendStreamRecord(tx *sql.Tx, record analytics.Record, build analytics.Build) (int64, error) {
	buildID, err := analyticsBuildID(tx, build)
	if err != nil {
		return 0, err
	}
	if err := tx.QueryRow("INSERT INTO analytics_sequences (stream, seq) VALUES (?, 1) ON CONFLICT(stream) DO UPDATE SET seq = seq + 1 RETURNING seq",
		record.Stream).Scan(&record.Seq); err != nil {
		return 0, err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec("INSERT INTO analytics_outbox (stream, seq, at, record, build_id) VALUES (?, ?, ?, ?, ?)",
		record.Stream, record.Seq, record.At.UTC().Format(analyticsTimeLayout), string(encoded), buildID); err != nil {
		return 0, err
	}
	return record.Seq, nil
}

func analyticsBuildID(tx *sql.Tx, build analytics.Build) (int64, error) {
	encoded, err := json.Marshal(build)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec("INSERT INTO analytics_builds (build) VALUES (?) ON CONFLICT(build) DO NOTHING", string(encoded)); err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow("SELECT id FROM analytics_builds WHERE build = ?", string(encoded)).Scan(&id)
	return id, err
}

func (s *Store) UnsentAnalyticsRecords(limit int, unrecorded analytics.Build, home string) (UnsentAnalytics, error) {
	unsent := UnsentAnalytics{Build: unrecorded}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return unsent, nil
	}
	unsent.Generation = s.analyticsGenerationLocked(home)
	var buildID sql.NullInt64
	var build sql.NullString
	switch err := s.db.QueryRow(`SELECT o.build_id, b.build FROM analytics_outbox o LEFT JOIN analytics_builds b ON b.id = o.build_id
		WHERE o.sent_at IS NULL ORDER BY o.id LIMIT 1`).Scan(&buildID, &build); {
	case errors.Is(err, sql.ErrNoRows):
		return unsent, nil
	case err != nil:
		return unsent, err
	}
	if build.Valid {
		unsent.Build = analytics.Build{}
		if err := json.Unmarshal([]byte(build.String), &unsent.Build); err != nil {
			return unsent, fmt.Errorf("analytics build %d: %w", buildID.Int64, err)
		}
	}
	rows, err := s.db.Query("SELECT id, record FROM analytics_outbox WHERE sent_at IS NULL AND build_id IS ? ORDER BY id LIMIT ?", buildID, limit)
	if err != nil {
		return unsent, err
	}
	defer rows.Close()
	for rows.Next() {
		var row AnalyticsRow
		if err := rows.Scan(&row.ID, &row.Record); err != nil {
			return unsent, err
		}
		unsent.Rows = append(unsent.Rows, row)
	}
	return unsent, rows.Err()
}

func (s *Store) MarkAnalyticsSent(records []analytics.Record, at time.Time) ([]Vent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, nil
	}
	sentAt := at.UTC().Format(analyticsTimeLayout)
	keys := make([][2]any, len(records))
	for i, record := range records {
		keys[i] = [2]any{record.Stream, record.Seq}
	}
	keyList, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`UPDATE vents SET sent_at = ? WHERE sent_at IS NULL AND outbox_seq IN (
		SELECT json_extract(value, '$[1]') FROM json_each(?) WHERE json_extract(value, '$[0]') = ?) RETURNING `+ventColumns,
		sentAt, string(keyList), analytics.StreamVent)
	if err != nil {
		return nil, err
	}
	sent, err := scanVents(rows)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE analytics_outbox SET sent_at = ? WHERE sent_at IS NULL AND (stream, seq) IN (
		SELECT json_extract(value, '$[0]'), json_extract(value, '$[1]') FROM json_each(?))`, sentAt, string(keyList)); err != nil {
		return nil, err
	}
	return sent, tx.Commit()
}

func (s *Store) DeleteUnsentAnalyticsRecords() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec("DELETE FROM analytics_outbox WHERE sent_at IS NULL AND stream != ?", analytics.StreamVent)
	return err
}

func (s *Store) ImportAnalyticsRecords(build analytics.Build, records []string, generation string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return fmt.Errorf("the analytics outbox needs the SQLite store")
	}
	if generation != s.analyticsGenerationLocked("") {
		return ErrStaleAnalyticsGeneration
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	buildID, err := analyticsBuildID(tx, build)
	if err != nil {
		return err
	}
	for _, encoded := range records {
		var record analytics.Record
		if err := json.Unmarshal([]byte(encoded), &record); err != nil {
			return fmt.Errorf("forwarded analytics record: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO analytics_outbox (stream, seq, at, record, build_id) VALUES (?, ?, ?, ?, ?) ON CONFLICT (stream, seq) DO NOTHING",
			record.Stream, record.Seq, record.At.UTC().Format(analyticsTimeLayout), encoded, buildID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AnalyticsRecords(profileID string, stream analytics.Stream, beforeID int64, limit int) (AnalyticsPage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return AnalyticsPage{}, nil
	}
	query := "SELECT id, record FROM analytics_outbox WHERE (stream != ? OR seq IN (SELECT outbox_seq FROM vents WHERE profile_id = ?))"
	args := []any{analytics.StreamVent, profileID}
	if stream != "" {
		query += " AND (stream = ? OR stream LIKE ? ESCAPE '\\')"
		args = append(args, stream, string(stream)+analytics.MachineStreamLike)
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

func (s *Store) PruneAnalyticsOutbox(before, unsentBefore time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return 0, nil
	}
	result, err := s.db.Exec("DELETE FROM analytics_outbox WHERE at < ? AND (sent_at IS NOT NULL OR at < ?)",
		before.UTC().Format(analyticsTimeLayout), unsentBefore.UTC().Format(analyticsTimeLayout))
	if err != nil {
		return 0, err
	}
	if _, err := s.db.Exec("DELETE FROM analytics_builds WHERE id NOT IN (SELECT build_id FROM analytics_outbox WHERE build_id IS NOT NULL)"); err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

type AnalyticsStatus struct {
	Records, Bytes, Waiting int64
	LastSentAt              time.Time
}

func (s *Store) AnalyticsStatus() (AnalyticsStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var status AnalyticsStatus
	if s.db == nil {
		return status, nil
	}
	var lastSent sql.NullString
	err := s.db.QueryRow("SELECT count(*), coalesce(sum(length(record)), 0), count(*) - count(sent_at), max(sent_at) FROM analytics_outbox").
		Scan(&status.Records, &status.Bytes, &status.Waiting, &lastSent)
	status.LastSentAt = parseStoreTime(lastSent.String)
	return status, err
}

func (s *Store) ClearAnalyticsOutbox() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return 0, nil
	}
	return clearAnalyticsOutbox(s.db)
}

func clearAnalyticsOutbox(db interface {
	Exec(string, ...any) (sql.Result, error)
}) (int64, error) {
	result, err := db.Exec("DELETE FROM analytics_outbox")
	if err != nil {
		return 0, err
	}
	if _, err := db.Exec("DELETE FROM analytics_builds"); err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) DatabaseBytes() (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return 0, nil
	}
	var size int64
	err := s.db.QueryRow("SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()").Scan(&size)
	return size, err
}

func (s *Store) CountDesktops() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return 0, nil
	}
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM desktops WHERE profile_id IN (SELECT id FROM profiles WHERE deleted_at = '')`).Scan(&count)
	return count, err
}
