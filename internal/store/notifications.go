package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// read_at holds '' while unread and a sortableTimeFormat stamp once read;
// parseStoreTime decodes a blank or garbage value to the zero time.

// A closed set: an unrecognized value must resolve to one of these rather than
// reach the app unstyled.
type NotificationSeverity string

const (
	NotificationInfo     NotificationSeverity = "info"
	NotificationWarning  NotificationSeverity = "warning"
	NotificationCritical NotificationSeverity = "critical"
)

func NormalizeNotificationSeverity(raw string) NotificationSeverity {
	switch NotificationSeverity(strings.ToLower(strings.TrimSpace(raw))) {
	case NotificationWarning:
		return NotificationWarning
	case NotificationCritical:
		return NotificationCritical
	default:
		return NotificationInfo
	}
}

type NotificationRecord struct {
	ID         string
	Kind       string
	Severity   NotificationSeverity
	Title      string
	Body       string
	Detail     string
	SourceKind string
	SourceID   string
	CreatedAt  time.Time
	ReadAt     time.Time
}

func (s *Store) AddNotification(rec NotificationRecord, now time.Time) (NotificationRecord, error) {
	if s.db == nil {
		return NotificationRecord{}, fmt.Errorf("store: no database")
	}
	rec.ID = uuid.NewString()
	rec.CreatedAt = now.UTC()
	rec.ReadAt = time.Time{}
	rec.Severity = NormalizeNotificationSeverity(string(rec.Severity))
	_, err := s.db.Exec(
		`INSERT INTO notifications (id, kind, severity, title, body, detail, source_kind, source_id, created_at, read_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '')`,
		rec.ID, rec.Kind, string(rec.Severity), rec.Title, rec.Body, rec.Detail, rec.SourceKind, rec.SourceID,
		rec.CreatedAt.Format(sortableTimeFormat),
	)
	if err != nil {
		return NotificationRecord{}, fmt.Errorf("store: add notification: %w", err)
	}
	return rec, nil
}

func (s *Store) ListNotifications() ([]NotificationRecord, error) {
	if s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	rows, err := s.db.Query(
		`SELECT id, kind, severity, title, body, detail, source_kind, source_id, created_at, read_at
		 FROM notifications ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: list notifications: %w", err)
	}
	defer rows.Close()
	var out []NotificationRecord
	for rows.Next() {
		rec, err := scanNotificationRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan notification: %w", err)
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

func (s *Store) UnreadNotificationCount() (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("store: no database")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE read_at = ''`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: unread notification count: %w", err)
	}
	return n, nil
}

// Count and title come from one statement so the ambient surface never mixes
// two instants.
func (s *Store) UnreadCriticalNotifications() (int, string, error) {
	if s.db == nil {
		return 0, "", fmt.Errorf("store: no database")
	}
	var (
		n     int
		title string
	)
	critical := string(NotificationCritical)
	if err := s.db.QueryRow(
		`SELECT COUNT(*),
		        COALESCE((SELECT title FROM notifications
		                  WHERE read_at = '' AND severity = ?
		                  ORDER BY created_at DESC, id DESC LIMIT 1), '')
		 FROM notifications WHERE read_at = '' AND severity = ?`,
		critical, critical).Scan(&n, &title); err != nil {
		return 0, "", fmt.Errorf("store: unread critical notifications: %w", err)
	}
	return n, title, nil
}

func (s *Store) MarkNotificationRead(id string, now time.Time) error {
	if s.db == nil {
		return fmt.Errorf("store: no database")
	}
	if _, err := s.db.Exec(
		`UPDATE notifications SET read_at = ? WHERE id = ? AND read_at = ''`,
		now.UTC().Format(sortableTimeFormat), id); err != nil {
		return fmt.Errorf("store: mark notification read %s: %w", id, err)
	}
	return nil
}

func (s *Store) MarkAllNotificationsRead(now time.Time) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("store: no database")
	}
	res, err := s.db.Exec(
		`UPDATE notifications SET read_at = ? WHERE read_at = ''`,
		now.UTC().Format(sortableTimeFormat))
	if err != nil {
		return 0, fmt.Errorf("store: mark all notifications read: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

func scanNotificationRow(sc rowScanner) (*NotificationRecord, error) {
	var (
		rec                              NotificationRecord
		severityStr, createdStr, readStr string
	)
	if err := sc.Scan(&rec.ID, &rec.Kind, &severityStr, &rec.Title, &rec.Body, &rec.Detail,
		&rec.SourceKind, &rec.SourceID, &createdStr, &readStr); err != nil {
		return nil, err
	}
	rec.Severity = NormalizeNotificationSeverity(severityStr)
	rec.CreatedAt = parseStoreTime(createdStr)
	rec.ReadAt = parseStoreTime(readStr)
	return &rec, nil
}
