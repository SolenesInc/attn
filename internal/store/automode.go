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
		cfg.Rules = automode.ResolveRules(nil)
		cfg.Network = automode.ResolveNetwork(wsPort, cfg.Network)
		return cfg
	}
	cfg := defaults()
	if s.db == nil {
		return cfg, nil
	}
	var (
		enabled                          int
		policy, sandbox                  string
		environment, rules, network, old string
	)
	err := q.QueryRow(`
		SELECT enabled_default, approval_policy, sandbox_mode, environment, rules, network, legacy_patterns
		FROM automode_config WHERE id = 1
	`).Scan(&enabled, &policy, &sandbox, &environment, &rules, &network, &old)
	if err == sql.ErrNoRows {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	cfg.EnabledDefault = enabled != 0
	if strings.TrimSpace(policy) != "" {
		cfg.ApprovalPolicy = policy
	}
	if strings.TrimSpace(sandbox) != "" {
		cfg.SandboxMode = sandbox
	}
	if cfg.Environment, err = decodeEnvironment(environment); err != nil {
		return defaults(), err
	}
	stored, err := decodeRules(rules)
	if err != nil {
		return defaults(), err
	}
	cfg.Rules = automode.ResolveRules(stored)
	storedNetwork, err := decodeNetwork(network)
	if err != nil {
		return defaults(), err
	}
	cfg.Network = automode.ResolveNetwork(wsPort, storedNetwork)
	if cfg.LegacyPatterns, err = decodeStringList(old, "legacy_patterns"); err != nil {
		return defaults(), err
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

func decodeRules(raw string) ([]automode.Rule, error) {
	if strings.TrimSpace(raw) == "" {
		return []automode.Rule{}, nil
	}
	var rules []automode.Rule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil, fmt.Errorf("automode config rules is not a JSON list: %w", err)
	}
	if rules == nil {
		rules = []automode.Rule{}
	}
	return rules, nil
}

func decodeNetwork(raw string) (automode.Network, error) {
	network := automode.DefaultNetwork()
	if strings.TrimSpace(raw) == "" {
		return network, nil
	}
	if err := json.Unmarshal([]byte(raw), &network); err != nil {
		return automode.DefaultNetwork(), fmt.Errorf("automode config network is not a JSON object: %w", err)
	}
	if network.AllowedDomains == nil {
		network.AllowedDomains = []string{}
	}
	if network.DeniedDomains == nil {
		network.DeniedDomains = []string{}
	}
	return network, nil
}

func (s *Store) SetAutoModeEnabledDefault(enabled bool, now time.Time) (automode.Config, error) {
	return s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		cfg.EnabledDefault = enabled
		return nil
	})
}

// A nil field is one the caller did not name, which leaves it as it stands.
func (s *Store) SetAutoModePolicy(approvalPolicy, sandboxMode *string, now time.Time) (automode.Config, error) {
	if approvalPolicy == nil && sandboxMode == nil {
		return automode.Config{}, fmt.Errorf(
			"automode policy takes an approval policy, a sandbox mode, or both; it was given neither")
	}
	if approvalPolicy != nil {
		if err := automode.ValidateApprovalPolicy(*approvalPolicy); err != nil {
			return automode.Config{}, err
		}
	}
	if sandboxMode != nil {
		if err := automode.ValidateSandboxMode(*sandboxMode); err != nil {
			return automode.Config{}, err
		}
	}
	return s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		if approvalPolicy != nil {
			cfg.ApprovalPolicy = *approvalPolicy
		}
		if sandboxMode != nil {
			cfg.SandboxMode = *sandboxMode
		}
		return nil
	})
}

func (s *Store) AddAutoModeRule(rule automode.Rule, now time.Time) (automode.Config, error) {
	rule = automode.NormalizeRule(rule)
	if err := automode.ValidateRule(rule); err != nil {
		return automode.Config{}, err
	}
	if automode.IsShippedRule(rule.Pattern) {
		return automode.Config{}, fmt.Errorf(
			"%q is a built-in rule and cannot be rewritten: it is what stops a session under auto "+
				"mode from rewriting its own policy", rule.Describe())
	}
	return s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		cfg.Rules = upsertRule(cfg.Rules, rule)
		return nil
	})
}

func (s *Store) RemoveAutoModeRule(pattern []automode.PatternToken, now time.Time) (automode.Config, error) {
	if len(pattern) == 0 {
		return automode.Config{}, fmt.Errorf("removing a rule needs the pattern it matches")
	}
	if automode.IsShippedRule(pattern) {
		return automode.Config{}, fmt.Errorf(
			"%q is a built-in rule and cannot be removed: it is what stops a session under auto "+
				"mode from rewriting its own policy", automode.Rule{Pattern: pattern}.Describe())
	}
	return s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		key := automode.PatternKey(pattern)
		kept := make([]automode.Rule, 0, len(cfg.Rules))
		found := false
		for _, rule := range cfg.Rules {
			if automode.PatternKey(rule.Pattern) == key {
				found = true
				continue
			}
			kept = append(kept, rule)
		}
		if !found {
			return fmt.Errorf("no rule matches %q", automode.Rule{Pattern: pattern}.Describe())
		}
		cfg.Rules = kept
		return nil
	})
}

func (s *Store) AddAutoModeHost(amendment automode.HostAmendment, now time.Time) (automode.Config, error) {
	amendment.Host = strings.TrimSpace(amendment.Host)
	if err := automode.ValidateHost(amendment); err != nil {
		return automode.Config{}, err
	}
	return s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		applyHostAmendment(&cfg.Network, amendment)
		return nil
	})
}

func (s *Store) RemoveAutoModeHost(amendment automode.HostAmendment, now time.Time) (automode.Config, error) {
	amendment.Host = strings.TrimSpace(amendment.Host)
	if err := automode.ValidateHost(amendment); err != nil {
		return automode.Config{}, err
	}
	if amendment.Decision == automode.HostDeny &&
		automode.IsShippedDomain(config.WSPort(), amendment.Host) {
		return automode.Config{}, fmt.Errorf(
			"%q is a built-in denied host and cannot be removed: it is this daemon's own control "+
				"port", amendment.Host)
	}
	return s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		list := &cfg.Network.AllowedDomains
		if amendment.Decision == automode.HostDeny {
			list = &cfg.Network.DeniedDomains
		}
		kept := make([]string, 0, len(*list))
		found := false
		for _, host := range *list {
			if host == amendment.Host {
				found = true
				continue
			}
			kept = append(kept, host)
		}
		if !found {
			return fmt.Errorf("%q is not in the %sed hosts", amendment.Host, amendment.Decision)
		}
		*list = kept
		return nil
	})
}

// A host holds one decision: allowing what was denied moves it rather than listing it twice.
func applyHostAmendment(network *automode.Network, amendment automode.HostAmendment) {
	network.AllowedDomains = withoutHost(network.AllowedDomains, amendment.Host)
	network.DeniedDomains = withoutHost(network.DeniedDomains, amendment.Host)
	if amendment.Decision == automode.HostAllow {
		network.AllowedDomains = append(network.AllowedDomains, amendment.Host)
		return
	}
	network.DeniedDomains = append(network.DeniedDomains, amendment.Host)
}

func withoutHost(hosts []string, host string) []string {
	kept := make([]string, 0, len(hosts))
	for _, existing := range hosts {
		if existing == host {
			continue
		}
		kept = append(kept, existing)
	}
	return kept
}

// A rule is its prefix: re-adding one with a new decision replaces it in place, which
// is what "don't ask again" means the second time a command is answered differently.
func upsertRule(rules []automode.Rule, rule automode.Rule) []automode.Rule {
	key := automode.PatternKey(rule.Pattern)
	for i, existing := range rules {
		if automode.PatternKey(existing.Pattern) == key {
			rules[i] = rule
			return rules
		}
	}
	return append(rules, rule)
}

func applyAutoModeAmendment(cfg *automode.Config, kind, value string) error {
	switch kind {
	case automode.KindRule:
		rule, err := automode.ParseRuleValue(value)
		if err != nil {
			return err
		}
		if automode.IsShippedRule(rule.Pattern) {
			return fmt.Errorf("%q is a built-in rule and cannot be rewritten", rule.Describe())
		}
		cfg.Rules = upsertRule(cfg.Rules, rule)
		return nil
	case automode.KindHost:
		amendment, err := automode.ParseHostValue(value)
		if err != nil {
			return err
		}
		applyHostAmendment(&cfg.Network, amendment)
		return nil
	default:
		return fmt.Errorf("unknown proposal kind %q (want %s or %s)", kind, automode.KindRule, automode.KindHost)
	}
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
	return s.insertAutoModeProposal(s.db, kind, target, value, proposedBy, now)
}

func (s *Store) insertAutoModeProposal(
	q interface {
		rowQuerier
		execer
	},
	kind, target, value, proposedBy string, now time.Time,
) (AutoModeProposal, error) {
	findPending := func() (AutoModeProposal, error) {
		return scanAutoModeProposal(q.QueryRow(`
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
	if err := q.QueryRow(`
		SELECT COUNT(*) FROM automode_proposals WHERE state = ? AND proposed_by = ?`,
		automode.StatePending, proposedBy).Scan(&pending); err != nil {
		return AutoModeProposal{}, err
	}
	if pending >= automode.MaxPendingProposalsPerProposer {
		return AutoModeProposal{}, fmt.Errorf(
			"%s already holds %d pending auto mode proposals (the cap is %d); "+
				"promote or discard some in the app before proposing more. Asked to add: %s",
			describeProposer(proposedBy), pending, automode.MaxPendingProposalsPerProposer,
			automode.DescribeProposal(kind, value))
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	res, err := q.Exec(`
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
	cfg, err := s.applyPendingProposal(tx, proposal, now)
	if err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	if err := tx.Commit(); err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	proposal.State = automode.StatePromoted
	proposal.ResolvedAt = now.UTC()
	return proposal, cfg, nil
}

// PromoteReportedAmendment is the pi relay's path: a human answered "don't ask again"
// inside the session, so the record and the promotion are one move.
func (s *Store) PromoteReportedAmendment(kind, value, proposedBy string, now time.Time) (AutoModeProposal, automode.Config, error) {
	if err := automode.ValidateProposal(kind, "", value); err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
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

	proposal, err := s.insertAutoModeProposal(tx, kind, "", value, proposedBy, now)
	if err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	cfg, err := s.applyPendingProposal(tx, proposal, now)
	if err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	if err := tx.Commit(); err != nil {
		return AutoModeProposal{}, automode.Config{}, err
	}
	proposal.State = automode.StatePromoted
	proposal.ResolvedAt = now.UTC()
	return proposal, cfg, nil
}

func (s *Store) applyPendingProposal(tx *sql.Tx, proposal AutoModeProposal, now time.Time) (automode.Config, error) {
	cfg, err := s.readAutoModeConfig(tx)
	if err != nil {
		return automode.Config{}, err
	}
	if err := applyAutoModeAmendment(&cfg, proposal.Kind, proposal.Value); err != nil {
		return automode.Config{}, err
	}
	if err := writeAutoModeConfig(tx, cfg, now); err != nil {
		return automode.Config{}, err
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	if _, err := tx.Exec(`
		UPDATE automode_proposals SET state = ?, resolved_at = ?
		WHERE state = ? AND kind = ? AND target = ? AND value = ?`,
		automode.StatePromoted, stamp,
		automode.StatePending, proposal.Kind, proposal.Target, proposal.Value); err != nil {
		return automode.Config{}, err
	}
	return cfg, nil
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
	// Ledger recovery can beat the relay. Check their shared identity under the insertion lock.
	existing, err := scanAutoModeDenial(s.db.QueryRow(`
		SELECT id, session_id, tool, signature, reason, rule, created_at
		FROM automode_denials WHERE session_id = ? AND signature = ? AND created_at = ?
		ORDER BY id LIMIT 1`, denial.SessionID, denial.Signature, now.UTC().Format(sortableTimeFormat)))
	if err == nil {
		return existing, 0, nil
	}
	if err != sql.ErrNoRows {
		return AutoModeDenial{}, 0, err
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
		d, err := scanAutoModeDenial(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func scanAutoModeDenial(row interface{ Scan(...any) error }) (AutoModeDenial, error) {
	var d AutoModeDenial
	var created string
	if err := row.Scan(&d.ID, &d.SessionID, &d.Tool, &d.Signature, &d.Reason, &d.Rule, &created); err != nil {
		return AutoModeDenial{}, err
	}
	d.CreatedAt = parseStoredTime(created)
	return d, nil
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
	// Every config here came out of a read, which resolved the shipped entries in;
	// persisting them would freeze today's list into the row.
	cfg.Rules = automode.StripShippedRules(cfg.Rules)
	cfg.Network = automode.StripShippedNetwork(config.WSPort(), cfg.Network)
	environment, err := encodeEnvironment(cfg.Environment)
	if err != nil {
		return err
	}
	rules, err := json.Marshal(cfg.Rules)
	if err != nil {
		return err
	}
	network, err := json.Marshal(cfg.Network)
	if err != nil {
		return err
	}
	legacy, err := encodeStringList(cfg.LegacyPatterns)
	if err != nil {
		return err
	}
	enabled := 0
	if cfg.EnabledDefault {
		enabled = 1
	}
	_, err = e.Exec(`
		INSERT INTO automode_config
			(id, enabled_default, approval_policy, sandbox_mode, environment, rules,
			 network, legacy_patterns, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled_default = excluded.enabled_default,
			approval_policy = excluded.approval_policy,
			sandbox_mode    = excluded.sandbox_mode,
			environment     = excluded.environment,
			rules           = excluded.rules,
			network         = excluded.network,
			legacy_patterns = excluded.legacy_patterns,
			updated_at      = excluded.updated_at
	`, enabled, cfg.ApprovalPolicy, cfg.SandboxMode, environment, string(rules),
		string(network), legacy, now.UTC().Format(sortableTimeFormat))
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
