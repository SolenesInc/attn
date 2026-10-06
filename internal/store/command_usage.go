package store

import (
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/victorarias/attn/internal/profiles"
)

type CommandUsage struct {
	CommandID  string
	Score      float64
	LastUsedAt time.Time
}

const commandUsageHalfLife = 14 * 24 * time.Hour

func decayedCommandScore(score float64, lastUsedAt, now time.Time) float64 {
	return score * math.Exp2(-max(0, now.Sub(lastUsedAt).Seconds())/commandUsageHalfLife.Seconds())
}

func (s *Store) RecordCommandUsage(profileID, commandID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return profiles.Errorf(profiles.CodeUnavailable, "command usage needs the SQLite store")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := loadLiveProfile(tx, profileID); err != nil {
		return err
	}
	var score float64
	var stamp string
	err = tx.QueryRow("SELECT score, last_used_at FROM command_usage WHERE profile_id = ? AND command_id = ?", profileID, commandID).Scan(&score, &stamp)
	now := time.Now().UTC()
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		last, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return err
		}
		if last.After(now) {
			now = last
		}
		score = decayedCommandScore(score, last, now)
	}
	_, err = tx.Exec("INSERT INTO command_usage(profile_id, command_id, score, last_used_at) VALUES(?, ?, ?, ?) ON CONFLICT(profile_id, command_id) DO UPDATE SET score = excluded.score, last_used_at = excluded.last_used_at", profileID, commandID, score+1, now.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetCommandUsage(profileID string) ([]CommandUsage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, profiles.Errorf(profiles.CodeUnavailable, "command usage needs the SQLite store")
	}
	if _, err := loadLiveProfile(s.db, profileID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query("SELECT command_id, score, last_used_at FROM command_usage WHERE profile_id = ? ORDER BY command_id", profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now().UTC()
	entries := make([]CommandUsage, 0)
	for rows.Next() {
		var entry CommandUsage
		var stamp string
		if err := rows.Scan(&entry.CommandID, &entry.Score, &stamp); err != nil {
			return nil, err
		}
		entry.LastUsedAt, err = time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return nil, err
		}
		entry.Score = decayedCommandScore(entry.Score, entry.LastUsedAt, now)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
