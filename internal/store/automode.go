package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/config"
)

type AutoModeProposal struct {
	ID         int64
	Kind       string
	Target     string
	Value      string
	ProposedBy string
	State      string
	CreatedAt  time.Time
	ResolvedAt time.Time
}

type AutoModeDenial struct {
	ID        int64
	SessionID string
	Tool      string
	Signature string
	Reason    string
	Rule      string
	CreatedAt time.Time
}

// AutoModeDenialRows is a tripwire, not a budget: auto mode's circuit breaker stops a
// session at 20 denials (plugins/attn-pi/automode/session.ts), so this is 25 episodes.
const AutoModeDenialRows = 500

func (s *Store) GetAutoModeConfig() (automode.Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readAutoModeConfig(s.db)
}

func (s *Store) readAutoModeConfig(q rowQuerier) (automode.Config, error) {
	wsPort := config.WSPort()
	defaults := func() automode.Config {
		cfg := automode.Defaults()
		cfg.HardDeny = automode.ResolveHardDeny(wsPort, nil)
		return cfg
	}
	cfg := defaults()
	if s.db == nil {
		return cfg, nil
	}
	var (
		enabled                      int
		environment, allow, hardDeny string
		models                       string
	)
	err := q.QueryRow(`
		SELECT enabled_default, environment, allow_patterns, hard_deny, models
		FROM automode_config WHERE id = 1
	`).Scan(&enabled, &environment, &allow, &hardDeny, &models)
	if err == sql.ErrNoRows {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	cfg.EnabledDefault = enabled != 0
	if cfg.Environment, err = decodeEnvironment(environment); err != nil {
		return defaults(), err
	}
	if cfg.Allow, err = decodeStringList(allow, "allow"); err != nil {
		return defaults(), err
	}
	stored, err := decodeStringList(hardDeny, "hard_deny")
	if err != nil {
		return defaults(), err
	}
	cfg.HardDeny = automode.ResolveHardDeny(wsPort, stored)
	if list, err := decodeStringList(models, "models"); err != nil {
		return defaults(), err
	} else if len(list) > 0 {
		cfg.Models = list
	}
	return cfg, nil
}

func (s *Store) SetAutoModeEnvironmentSlot(id string, values []string, now time.Time) (automode.Config, error) {
	return s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		return cfg.Environment.SetSlot(id, values)
	})
}

func (s *Store) SetAutoModeEnvironmentNotes(notes []string, now time.Time) (automode.Config, error) {
	return s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		cfg.Environment.Notes = append([]string{}, notes...)
		return nil
	})
}

func encodeEnvironment(env automode.Environment) (string, error) {
	encoded, err := json.Marshal(env.Normalize())
	if err != nil {
		return "", fmt.Errorf("encode environment: %w", err)
	}
	return string(encoded), nil
}

func decodeEnvironment(raw string) (automode.Environment, error) {
	env := automode.NewEnvironment()
	if strings.TrimSpace(raw) == "" {
		return env, nil
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return automode.NewEnvironment(), fmt.Errorf("decode environment %q: %w", raw, err)
	}
	if env.Slots == nil {
		env.Slots = map[string][]string{}
	}
	if env.Notes == nil {
		env.Notes = []string{}
	}
	return env, nil
}

func (s *Store) SetAutoModeEnabledDefault(enabled bool, now time.Time) (automode.Config, error) {
	return s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		cfg.EnabledDefault = enabled
		return nil
	})
}

// An empty list is auto mode off, which a caller may mean; it is not an error.
func (s *Store) SetAutoModeModels(models []string, now time.Time) (automode.Config, error) {
	parsed, err := automode.ParseModelList(automode.FormatModelList(models))
	if err != nil {
		return automode.Config{}, err
	}
	return s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		cfg.Models = parsed
		return nil
	})
}

func (s *Store) AddAutoModePattern(list, pattern string, now time.Time) (automode.Config, error) {
	pattern = strings.TrimSpace(pattern)
	if err := automode.ValidatePattern(list, pattern); err != nil {
		return automode.Config{}, err
	}
	return s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		values := autoModePatternList(cfg, list)
		for _, existing := range *values {
			if existing == pattern {
				return fmt.Errorf("%q is already in the %s list", pattern, list)
			}
		}
		*values = append(*values, pattern)
		return nil
	})
}

func (s *Store) RemoveAutoModePattern(list, pattern string, now time.Time) (automode.Config, error) {
	pattern = strings.TrimSpace(pattern)
	if list != automode.ListAllow && list != automode.ListHardDeny {
		return automode.Config{}, fmt.Errorf("unknown pattern list %q (want %s or %s)",
			list, automode.ListAllow, automode.ListHardDeny)
	}
	if list == automode.ListHardDeny {
		for _, shipped := range automode.ShippedHardDeny(config.WSPort()) {
			if shipped == pattern {
				return automode.Config{}, fmt.Errorf(
					"%q is a built-in hard deny and cannot be removed: it is what stops a session "+
						"under auto mode from rewriting its own policy", pattern)
			}
		}
	}
	return s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		values := autoModePatternList(cfg, list)
		kept := make([]string, 0, len(*values))
		found := false
		for _, existing := range *values {
			if existing == pattern {
				found = true
				continue
			}
			kept = append(kept, existing)
		}
		if !found {
			return fmt.Errorf("%q is not in the %s list", pattern, list)
		}
		*values = kept
		return nil
	})
}

func autoModePatternList(cfg *automode.Config, list string) *[]string {
	if list == automode.ListAllow {
		return &cfg.Allow
	}
	return &cfg.HardDeny
}

func (s *Store) CreateAutoModeProposal(kind, target, value, proposedBy string, now time.Time) (AutoModeProposal, error) {
	if err := automode.ValidateProposal(kind, target, value); err != nil {
		return AutoModeProposal{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return AutoModeProposal{}, fmt.Errorf("store: no database")
	}
	findPending := func() (AutoModeProposal, error) {
		return scanAutoModeProposal(s.db.QueryRow(`
			SELECT id, kind, target, value, proposed_by, state, created_at, resolved_at
			FROM automode_proposals
			WHERE state = ? AND kind = ? AND target = ? AND value = ? AND proposed_by = ?
			ORDER BY id ASC LIMIT 1`,
			automode.StatePending, kind, target, value, proposedBy))
	}
	existing, err := findPending()
	if err == nil {
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return AutoModeProposal{}, err
	}
	var pending int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM automode_proposals WHERE state = ? AND proposed_by = ?`,
		automode.StatePending, proposedBy).Scan(&pending); err != nil {
		return AutoModeProposal{}, err
	}
	if pending >= automode.MaxPendingProposalsPerProposer {
		return AutoModeProposal{}, fmt.Errorf(
			"%s already holds %d pending auto mode proposals (the cap is %d); "+
				"promote or discard some in the app before proposing more. Asked to add: %s",
			describeProposer(proposedBy), pending, automode.MaxPendingProposalsPerProposer,
			describeProposedChange(kind, target, value))
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	res, err := s.db.Exec(`
		INSERT INTO automode_proposals (kind, target, value, proposed_by, state, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, kind, target, value, proposedBy, automode.StatePending, stamp)
	if err != nil {

		if existing, findErr := findPending(); findErr == nil {
			return existing, nil
		}
		return AutoModeProposal{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return AutoModeProposal{}, err
	}
	return AutoModeProposal{
		ID: id, Kind: kind, Target: target, Value: value,
		ProposedBy: proposedBy, State: automode.StatePending, CreatedAt: now.UTC(),
	}, nil
}

func (s *Store) ListAutoModeProposals(state string) ([]AutoModeProposal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	query := `SELECT id, kind, target, value, proposed_by, state, created_at, resolved_at
		FROM automode_proposals`
	args := []any{}
	if state != "" {
		query += ` WHERE state = ?`
		args = append(args, state)
	}
	query += ` ORDER BY id ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutoModeProposal
	for rows.Next() {
		p, err := scanAutoModeProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) PromoteAutoModeProposal(id int64, now time.Time) (AutoModeProposal, automode.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return AutoModeProposal{}, automode.Config{}, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	defer tx.Rollback()

	proposal, err := scanAutoModeProposal(tx.QueryRow(`
		SELECT id, kind, target, value, proposed_by, state, created_at, resolved_at
		FROM automode_proposals WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return AutoModeProposal{}, automode.Config{}, fmt.Errorf("no auto mode proposal %d", id)
	}
	if err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	if proposal.State != automode.StatePending {
		return AutoModeProposal{}, automode.Config{}, fmt.Errorf("auto mode proposal %d is already %s", id, proposal.State)
	}
	if err := automode.ValidateProposal(proposal.Kind, proposal.Target, proposal.Value); err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}

	cfg, err := s.readAutoModeConfig(tx)
	if err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	switch proposal.Kind {
	case automode.KindAllow:
		cfg.Allow = appendUnique(cfg.Allow, proposal.Value)
	case automode.KindDeny:
		cfg.HardDeny = appendUnique(cfg.HardDeny, proposal.Value)
	case automode.KindModel:
		models, err := automode.ParseModelList(proposal.Value)
		if err != nil {
			return AutoModeProposal{}, automode.Config{}, err
		}
		cfg.Models = models
	}
	if err := writeAutoModeConfig(tx, cfg, now); err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	if _, err := tx.Exec(`
		UPDATE automode_proposals SET state = ?, resolved_at = ?
		WHERE state = ? AND kind = ? AND target = ? AND value = ?`,
		automode.StatePromoted, stamp,
		automode.StatePending, proposal.Kind, proposal.Target, proposal.Value); err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	if err := tx.Commit(); err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	proposal.State = automode.StatePromoted
	proposal.ResolvedAt = now.UTC()
	return proposal, cfg, nil
}

func (s *Store) DiscardAutoModeProposal(id int64, now time.Time) (AutoModeProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return AutoModeProposal{}, fmt.Errorf("store: no database")
	}
	proposal, err := scanAutoModeProposal(s.db.QueryRow(`
		SELECT id, kind, target, value, proposed_by, state, created_at, resolved_at
		FROM automode_proposals WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return AutoModeProposal{}, fmt.Errorf("no auto mode proposal %d", id)
	}
	if err != nil {
		return AutoModeProposal{}, err
	}
	if proposal.State != automode.StatePending {
		return AutoModeProposal{}, fmt.Errorf("auto mode proposal %d is already %s", id, proposal.State)
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	if _, err := s.db.Exec(`UPDATE automode_proposals SET state = ?, resolved_at = ? WHERE id = ?`,
		automode.StateDiscarded, stamp, id); err != nil {
		return AutoModeProposal{}, err
	}
	proposal.State = automode.StateDiscarded
	proposal.ResolvedAt = now.UTC()
	return proposal, nil
}

func (s *Store) RecordAutoModeDenial(denial AutoModeDenial, now time.Time) (AutoModeDenial, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return AutoModeDenial{}, 0, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return AutoModeDenial{}, 0, err
	}
	defer tx.Rollback()

	denial.CreatedAt = now.UTC()
	res, err := tx.Exec(`
		INSERT INTO automode_denials (session_id, tool, signature, reason, rule, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, denial.SessionID, denial.Tool, denial.Signature, denial.Reason, denial.Rule,
		denial.CreatedAt.Format(sortableTimeFormat))
	if err != nil {
		return AutoModeDenial{}, 0, err
	}
	if denial.ID, err = res.LastInsertId(); err != nil {
		return AutoModeDenial{}, 0, err
	}
	trimmed, err := tx.Exec(`
		DELETE FROM automode_denials
		WHERE id <= (SELECT id FROM automode_denials ORDER BY id DESC LIMIT 1 OFFSET ?)`,
		AutoModeDenialRows)
	if err != nil {
		return AutoModeDenial{}, 0, err
	}
	dropped, err := trimmed.RowsAffected()
	if err != nil {
		return AutoModeDenial{}, 0, err
	}
	if err := tx.Commit(); err != nil {
		return AutoModeDenial{}, 0, err
	}
	return denial, dropped, nil
}

func (s *Store) ListAutoModeDenials(limit int) ([]AutoModeDenial, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, session_id, tool, signature, reason, rule, created_at
		FROM automode_denials ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutoModeDenial
	for rows.Next() {
		var d AutoModeDenial
		var created string
		if err := rows.Scan(&d.ID, &d.SessionID, &d.Tool, &d.Signature, &d.Reason, &d.Rule, &created); err != nil {
			return nil, err
		}
		d.CreatedAt = parseStoredTime(created)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) mutateAutoModeConfig(now time.Time, apply func(*automode.Config) error) (automode.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return automode.Config{}, fmt.Errorf("store: no database")
	}
	cfg, err := s.readAutoModeConfig(s.db)
	if err != nil {
		return automode.Config{}, err
	}
	if err := apply(&cfg); err != nil {
		return automode.Config{}, err
	}
	if err := writeAutoModeConfig(s.db, cfg, now); err != nil {
		return automode.Config{}, err
	}
	return cfg, nil
}

func writeAutoModeConfig(e execer, cfg automode.Config, now time.Time) error {
	// Every config here came out of a read, which resolved the shipped denies in;
	// persisting them would freeze today's list into the row.
	cfg.HardDeny = automode.StripShippedHardDeny(config.WSPort(), cfg.HardDeny)
	environment, err := encodeEnvironment(cfg.Environment)
	if err != nil {
		return err
	}
	allow, err := encodeStringList(cfg.Allow)
	if err != nil {
		return err
	}
	hardDeny, err := encodeStringList(cfg.HardDeny)
	if err != nil {
		return err
	}
	models, err := encodeStringList(cfg.Models)
	if err != nil {
		return err
	}
	enabled := 0
	if cfg.EnabledDefault {
		enabled = 1
	}
	_, err = e.Exec(`
		INSERT INTO automode_config
			(id, enabled_default, environment, allow_patterns, hard_deny,
			 models, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled_default = excluded.enabled_default,
			environment     = excluded.environment,
			allow_patterns  = excluded.allow_patterns,
			hard_deny       = excluded.hard_deny,
			models          = excluded.models,
			updated_at      = excluded.updated_at
	`, enabled, environment, allow, hardDeny, models,
		now.UTC().Format(sortableTimeFormat))
	return err
}

func scanAutoModeProposal(row interface{ Scan(...any) error }) (AutoModeProposal, error) {
	var p AutoModeProposal
	var created, resolved string
	if err := row.Scan(&p.ID, &p.Kind, &p.Target, &p.Value, &p.ProposedBy, &p.State, &created, &resolved); err != nil {
		return AutoModeProposal{}, err
	}
	p.CreatedAt = parseStoredTime(created)
	p.ResolvedAt = parseStoredTime(resolved)
	return p, nil
}

func describeProposer(proposedBy string) string {
	if proposedBy == "" {
		return "this caller"
	}
	return proposedBy
}

func describeProposedChange(kind, target, value string) string {
	if kind == automode.KindModel {
		if value == "" {
			return fmt.Sprintf("%s %s (none, which leaves auto mode off)", kind, target)
		}
		return fmt.Sprintf("%s %s %s", kind, target, value)
	}
	return fmt.Sprintf("%s %s", kind, value)
}

func parseStoredTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{sortableTimeFormat, time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func encodeStringList(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeStringList(raw, field string) ([]string, error) {
	if raw == "" {
		return []string{}, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("automode config %s is not a JSON list: %w", field, err)
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
