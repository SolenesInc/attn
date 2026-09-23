package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/delegationprefs"
)

func applyMigration152(tx *sql.Tx) error {
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS delegation_preference_revisions (
	revision INTEGER PRIMARY KEY,
	parent INTEGER,
	restores INTEGER,
	config TEXT NOT NULL,
	origin TEXT NOT NULL DEFAULT '',
	source_session TEXT NOT NULL DEFAULT '',
	message TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
)`); err != nil {
		return err
	}
	carried, err := tableExists(tx, "delegation_preferences")
	if err != nil || !carried {
		return err
	}
	_, err = tx.Exec(`
INSERT INTO delegation_preference_revisions (revision, config, created_at)
SELECT MAX(1, COALESCE(CASE WHEN json_valid(config) THEN json_extract(config, '$.revision') END, 1)),
	config, strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
FROM delegation_preferences WHERE id = 1;
DROP TABLE delegation_preferences;`)
	return err
}

type DelegationPreferencesNote struct {
	Origin        string
	SourceSession string
	Message       string
}

type DelegationPreferencesRevision struct {
	Config   delegationprefs.Config
	Previous *delegationprefs.Config
	Restores *int
	DelegationPreferencesNote
	CreatedAt time.Time
}

type delegationPreferencesRow struct {
	revision DelegationPreferencesRevision
	parent   *int
}

const delegationPreferencesRowColumns = `revision, parent, restores, config, origin, source_session, message, created_at`

func (s *Store) GetDelegationPreferences() (delegationprefs.Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readDelegationPreferences(s.db)
}

func (s *Store) readDelegationPreferences(q rowQuerier) (delegationprefs.Config, error) {
	live, err := s.readLiveDelegationPreferences(q)
	return live.revision.Config, err
}

func (s *Store) readLiveDelegationPreferences(q rowQuerier) (delegationPreferencesRow, error) {
	if s.db == nil {
		return delegationPreferencesRow{revision: DelegationPreferencesRevision{Config: delegationprefs.Defaults()}}, errors.New("preferences database is unavailable")
	}
	row, found, err := scanDelegationPreferencesRow(q.QueryRow(`SELECT ` + delegationPreferencesRowColumns + ` FROM delegation_preference_revisions ORDER BY revision DESC LIMIT 1`))
	if err != nil || found {
		return row, err
	}
	return delegationPreferencesRow{revision: DelegationPreferencesRevision{Config: delegationprefs.Defaults()}}, nil
}

func readDelegationPreferencesRevision(q rowQuerier, revision int) (delegationPreferencesRow, bool, error) {
	if revision == 0 {
		return delegationPreferencesRow{revision: DelegationPreferencesRevision{Config: delegationprefs.Defaults()}}, true, nil
	}
	return scanDelegationPreferencesRow(q.QueryRow(`SELECT `+delegationPreferencesRowColumns+` FROM delegation_preference_revisions WHERE revision = ?`, revision))
}

func scanDelegationPreferencesRow(row interface{ Scan(...any) error }) (delegationPreferencesRow, bool, error) {
	var (
		out              delegationPreferencesRow
		revision         int
		parent, restores sql.NullInt64
		raw, createdAt   string
	)
	out.revision.Config = delegationprefs.Defaults()
	err := row.Scan(&revision, &parent, &restores, &raw, &out.revision.Origin, &out.revision.SourceSession, &out.revision.Message, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	cfg, err := decodeDelegationPreferences(raw, revision)
	if err != nil {
		return out, false, err
	}
	out.revision.Config = cfg
	out.revision.CreatedAt = parseStoreTime(createdAt)
	out.parent = nullableInt(parent)
	out.revision.Restores = nullableInt(restores)
	return out, true, nil
}

func nullableInt(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	n := int(value.Int64)
	return &n
}

func decodeDelegationPreferences(raw string, revision int) (delegationprefs.Config, error) {
	cfg := delegationprefs.Defaults()
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return cfg, fmt.Errorf("read delegation preferences revision %d: %w", revision, err)
	}
	cfg.Revision = revision
	if err := delegationprefs.Validate(cfg); err != nil {
		return cfg, fmt.Errorf("invalid stored delegation preferences revision %d: %w", revision, err)
	}
	if cfg.Roles == nil {
		cfg.Roles = []delegationprefs.Role{}
	}
	return cfg, nil
}

func (s *Store) SaveDelegationPreferences(cfg delegationprefs.Config, note DelegationPreferencesNote) (DelegationPreferencesRevision, error) {
	if err := delegationprefs.Validate(cfg); err != nil {
		return DelegationPreferencesRevision{Config: cfg}, err
	}
	return s.appendDelegationPreferences(func(tx *sql.Tx, live delegationPreferencesRow) (delegationprefs.Config, *int, *int, error) {
		if cfg.Revision != live.revision.Config.Revision {
			return live.revision.Config, nil, nil, delegationprefs.ErrConflict
		}
		return cfg, &live.revision.Config.Revision, nil, nil
	}, note)
}

func (s *Store) RollbackDelegationPreferences(target, expected *int, note DelegationPreferencesNote) (DelegationPreferencesRevision, error) {
	return s.appendDelegationPreferences(func(tx *sql.Tx, live delegationPreferencesRow) (delegationprefs.Config, *int, *int, error) {
		current := live.revision.Config.Revision
		if expected != nil && *expected != current {
			return live.revision.Config, nil, nil, delegationprefs.ErrConflict
		}
		if target == nil {
			if live.parent == nil {
				return live.revision.Config, nil, nil, fmt.Errorf("revision %d is where the history starts; there is nothing to roll back to", current)
			}
			restored, found, err := readDelegationPreferencesRevision(tx, *live.parent)
			if err != nil {
				return live.revision.Config, nil, nil, err
			}
			if !found {
				return live.revision.Config, nil, nil, fmt.Errorf("revision %d is missing from the history", *live.parent)
			}
			return restored.revision.Config, restored.parent, live.parent, nil
		}
		if *target == current {
			return live.revision.Config, nil, nil, fmt.Errorf("revision %d is already live", current)
		}
		restored, found, err := readDelegationPreferencesRevision(tx, *target)
		if err != nil {
			return live.revision.Config, nil, nil, err
		}
		if !found {
			return live.revision.Config, nil, nil, fmt.Errorf("no revision %d; attn delegate roles history lists them", *target)
		}
		return restored.revision.Config, &current, target, nil
	}, note)
}

func (s *Store) appendDelegationPreferences(
	next func(*sql.Tx, delegationPreferencesRow) (cfg delegationprefs.Config, parent, restores *int, err error),
	note DelegationPreferencesNote,
) (DelegationPreferencesRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return DelegationPreferencesRevision{Config: delegationprefs.Defaults()}, errors.New("preferences database is unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return DelegationPreferencesRevision{}, err
	}
	defer tx.Rollback()
	live, err := s.readLiveDelegationPreferences(tx)
	if err != nil {
		return live.revision, err
	}
	cfg, parent, restores, err := next(tx, live)
	if err != nil {
		return DelegationPreferencesRevision{Config: cfg}, err
	}
	previous := live.revision.Config
	cfg.Revision = previous.Revision + 1
	if cfg.Roles == nil {
		cfg.Roles = []delegationprefs.Role{}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return DelegationPreferencesRevision{Config: cfg}, err
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(`INSERT INTO delegation_preference_revisions (`+delegationPreferencesRowColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		cfg.Revision, parent, restores, string(raw), note.Origin, note.SourceSession, note.Message, now.Format(sortableTimeFormat)); err != nil {
		return DelegationPreferencesRevision{Config: cfg}, err
	}
	if err := tx.Commit(); err != nil {
		return DelegationPreferencesRevision{Config: cfg}, err
	}
	return DelegationPreferencesRevision{Config: cfg, Previous: &previous, Restores: restores, DelegationPreferencesNote: note, CreatedAt: now}, nil
}

func (s *Store) DelegationPreferencesHistory(limit int) ([]DelegationPreferencesRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, errors.New("preferences database is unavailable")
	}
	rows, err := s.db.Query(`SELECT `+delegationPreferencesRowColumns+` FROM delegation_preference_revisions ORDER BY revision DESC LIMIT ?`, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var history []DelegationPreferencesRevision
	for rows.Next() {
		row, _, err := scanDelegationPreferencesRow(rows)
		if err != nil {
			return nil, err
		}
		history = append(history, row.revision)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range history {
		switch revision := history[i].Config.Revision; {
		case revision == 1:
			defaults := delegationprefs.Defaults()
			history[i].Previous = &defaults
		case i+1 < len(history) && history[i+1].Config.Revision == revision-1:
			history[i].Previous = &history[i+1].Config
		}
	}
	return history[:min(limit, len(history))], nil
}
