package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automode"
)

func userRules(resolved []automode.Rule) []automode.Rule {
	return automode.StripShippedRules(resolved)
}

func ruleLines(rules []automode.Rule) []string {
	lines := make([]string, 0, len(rules))
	for _, rule := range rules {
		lines = append(lines, rule.Decision+" "+rule.Describe())
	}
	return lines
}

func plantPre140AutoModeConfig(t *testing.T, s *Store, dbPath, environment, allow, hardDeny string, from int) {
	t.Helper()
	for _, stmt := range []string{
		`ALTER TABLE automode_config DROP COLUMN approval_policy`,
		`ALTER TABLE automode_config DROP COLUMN sandbox_mode`,
		`ALTER TABLE automode_config DROP COLUMN rules`,
		`ALTER TABLE automode_config DROP COLUMN network`,
		`ALTER TABLE automode_config DROP COLUMN legacy_patterns`,
		`ALTER TABLE automode_config ADD COLUMN allow_patterns TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE automode_config ADD COLUMN hard_deny TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE automode_config ADD COLUMN models TEXT NOT NULL DEFAULT '[]'`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("plant the pre-140 schema (%s): %v", stmt, err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO automode_config
		(id, enabled_default, environment, allow_patterns, hard_deny, models, updated_at)
		VALUES (1, 1, ?, ?, ?, '[]', '2026-09-01T09:00:00Z')`,
		environment, allow, hardDeny); err != nil {
		t.Fatalf("plant the pre-140 row: %v", err)
	}
	if _, err := s.GetAutoModeConfig(); err == nil {
		t.Fatal("the planted schema already reads; this test would pass without the migration")
	}

	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= ?`, from); err != nil {
		t.Fatalf("unrecord migration %d: %v", from, err)
	}
	if err := migrateDBThrough(s.db, dbPath, 161); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}
}

func TestMigration140TurnsGlobsIntoRulesAndKeepsTheRest(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := newStoreAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	if _, err := s.db.Exec(`INSERT INTO automode_proposals
		(kind, target, value, proposed_by, state, created_at)
		VALUES ('allow', '', 'git status*', 'session-a', 'pending', '2026-09-01T08:00:00Z')`); err != nil {
		t.Fatalf("plant an old proposal: %v", err)
	}
	plantPre140AutoModeConfig(t, s, dbPath, `{"slots":{},"notes":[]}`,
		`["git status*","rm -rf /","gh pr create *"]`,
		`["*curl*","ssh prod","terraform apply *"]`, 140)

	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config after the migration: %v", err)
	}
	got := ruleLines(userRules(cfg.Rules))
	want := []string{
		"allow rm -rf /", "allow gh pr create",
		"forbidden ssh prod", "forbidden terraform apply",
	}
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Errorf("rules = %v, want %v", got, want)
	}
	for _, rule := range userRules(cfg.Rules) {
		if rule.Decision == automode.DecisionForbidden && rule.Justification == "" {
			t.Errorf("converted forbidden rule %q refuses without saying why", rule.Describe())
		}
	}
	if strings.Join(cfg.LegacyPatterns, "; ") != "git status*; *curl*" {
		t.Errorf("legacy patterns = %v, want the two globs no prefix rule can express", cfg.LegacyPatterns)
	}
	if cfg.ApprovalPolicy != automode.PolicyOnRequest || cfg.SandboxMode != automode.SandboxWorkspaceWrite {
		t.Errorf("policy = %q/%q, want the defaults", cfg.ApprovalPolicy, cfg.SandboxMode)
	}
	for _, column := range []string{"allow_patterns", "hard_deny", "models"} {
		var rows int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('automode_config') WHERE name = ?`, column).Scan(&rows); err != nil {
			t.Fatalf("read table info: %v", err)
		}
		if rows != 0 {
			t.Errorf("column %s survived the migration; two spellings of one setting is how one goes stale", column)
		}
	}
	pending, err := s.ListAutoModeProposals(automode.StatePending)
	if err != nil {
		t.Fatalf("list proposals: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %+v, want the old-kind proposal closed: nothing could promote it", pending)
	}
	if _, err := s.DismissAutoModeLegacyPattern("*curl*", time.Now()); err != nil {
		t.Fatalf("dismiss a carried legacy pattern: %v", err)
	}
	if again, err := s.GetAutoModeConfig(); err != nil || strings.Join(again.LegacyPatterns, "; ") != "git status*" {
		t.Errorf("legacy patterns after dismissing *curl* = %v (%v), want only git status*", again.LegacyPatterns, err)
	}
	if _, err := s.DismissAutoModeLegacyPattern("*curl*", time.Now()); err == nil {
		t.Error("dismissing a pattern no longer on the list was accepted")
	}
}

func TestMigration125KeepsTheOldProseAsNotes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := newStoreAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	plantPre140AutoModeConfig(t, s, dbPath, `["this laptop is mine","nothing here serves traffic"]`, `[]`, `[]`, 125)

	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config after the migration: %v", err)
	}
	if len(cfg.Environment.Notes) != 2 || cfg.Environment.Notes[0] != "this laptop is mine" {
		t.Errorf("notes = %v, want the prose that was there", cfg.Environment.Notes)
	}
	if filled, _ := cfg.Environment.Filled(); filled != 0 {
		t.Errorf("%d slots came up filled; prose cannot be read as a trust list", filled)
	}
}

func TestANetworkRowWrittenBeforeLocalBindingReadsAsOff(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	if _, err := s.SetAutoModeEnabledDefault(true, now); err != nil {
		t.Fatalf("seed the row: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE automode_config SET network = ? WHERE id = 1`,
		`{"enabled":true,"allowed_domains":["crates.io"],"denied_domains":[]}`); err != nil {
		t.Fatalf("plant the old network JSON: %v", err)
	}
	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if cfg.Network.AllowLocalBinding {
		t.Error("a row written before allow_local_binding read as allowed")
	}
	if len(cfg.Network.AllowedDomains) != 1 || cfg.Network.AllowedDomains[0] != "crates.io" {
		t.Errorf("allowed = %v, want the old row's hosts", cfg.Network.AllowedDomains)
	}
}
