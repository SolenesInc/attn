package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/config"
)

func userRules(resolved []automode.Rule) []automode.Rule {
	return automode.StripShippedRules(resolved)
}

func ruleValue(t *testing.T, decision, justification string, tokens ...string) string {
	t.Helper()
	value, err := automode.FormatRuleValue(automode.Rule{
		Pattern:       automode.Tokens(tokens...),
		Decision:      decision,
		Justification: justification,
	})
	if err != nil {
		t.Fatalf("format rule: %v", err)
	}
	return value
}

func hostValue(t *testing.T, host, decision string) string {
	t.Helper()
	value, err := automode.FormatHostValue(automode.HostAmendment{Host: host, Decision: decision})
	if err != nil {
		t.Fatalf("format host: %v", err)
	}
	return value
}

func ruleLines(rules []automode.Rule) []string {
	lines := make([]string, 0, len(rules))
	for _, rule := range rules {
		lines = append(lines, rule.Decision+" "+rule.Describe())
	}
	return lines
}

func TestAutoModeConfigDefaultsOnAFreshDatabase(t *testing.T) {
	s := New()
	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if !cfg.EnabledDefault {
		t.Error("enabled_default = false on a fresh database, want true")
	}
	if cfg.ApprovalPolicy != automode.PolicyOnRequest || cfg.SandboxMode != automode.SandboxWorkspaceWrite {
		t.Errorf("policy = %q/%q, want Codex's defaults", cfg.ApprovalPolicy, cfg.SandboxMode)
	}
	if filled, _ := cfg.Environment.Filled(); filled != 0 || len(cfg.LegacyPatterns) != 0 {
		t.Errorf("fresh config is not empty: %+v", cfg)
	}
	if got := userRules(cfg.Rules); len(got) != 0 {
		t.Errorf("rules = %v, want exactly the shipped ones", ruleLines(cfg.Rules))
	}
	if !cfg.Network.Enabled || len(cfg.Network.AllowedDomains) != 0 {
		t.Errorf("network = %+v, want on with nothing allowed yet", cfg.Network)
	}
	shipped := automode.ShippedDeniedDomains(config.WSPort())
	if len(cfg.Network.DeniedDomains) != len(shipped) {
		t.Errorf("denied domains = %v, want exactly the shipped ones", cfg.Network.DeniedDomains)
	}
}

func TestAutoModeEnvironmentRoundTrips(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	cfg, err := s.SetAutoModeEnvironmentSlot("domains", []string{"grafana.acme.corp", "  ", "grafana.acme.corp"}, now)
	if err != nil {
		t.Fatalf("set slot: %v", err)
	}
	if got := cfg.Environment.Slots["domains"]; len(got) != 1 || got[0] != "grafana.acme.corp" {
		t.Fatalf("domains = %v, want the one entry, trimmed and de-duplicated", got)
	}
	if _, err := s.SetAutoModeEnvironmentNotes([]string{"the CI box shares this checkout"}, now); err != nil {
		t.Fatalf("set notes: %v", err)
	}
	read, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if got := read.Environment.Slots["domains"]; len(got) != 1 {
		t.Errorf("domains did not survive the round trip: %v", got)
	}
	if len(read.Environment.Notes) != 1 {
		t.Errorf("notes = %v, want the line that was written", read.Environment.Notes)
	}
}

func TestAutoModeEnvironmentSlotRefusesWhatTheSchemaDoesNotHave(t *testing.T) {
	s := New()
	if _, err := s.SetAutoModeEnvironmentSlot("not_a_slot", []string{"x"}, time.Now()); err == nil {
		t.Fatal("an unknown slot was accepted")
	}
}

func TestAutoModeProposalDoesNotChangeTheConfig(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	before, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if _, err := s.CreateAutoModeProposal(
		automode.KindRule, "", ruleValue(t, automode.DecisionAllow, "", "git", "push"), "session-1", now,
	); err != nil {
		t.Fatalf("create rule proposal: %v", err)
	}
	if _, err := s.CreateAutoModeProposal(
		automode.KindHost, "", hostValue(t, "github.com", automode.HostAllow), "", now,
	); err != nil {
		t.Fatalf("create host proposal: %v", err)
	}
	after, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if len(userRules(after.Rules)) != 0 {
		t.Errorf("rules changed to %v", ruleLines(after.Rules))
	}
	if len(after.Network.AllowedDomains) != len(before.Network.AllowedDomains) {
		t.Errorf("allowed domains changed to %v", after.Network.AllowedDomains)
	}
	pending, err := s.ListAutoModeProposals(automode.StatePending)
	if err != nil {
		t.Fatalf("list proposals: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending proposals = %d, want 2", len(pending))
	}
	if pending[0].ProposedBy != "session-1" {
		t.Errorf("proposed_by = %q, want the recorded session", pending[0].ProposedBy)
	}
}

func TestAutoModeCreateProposalRefusesWhatCouldNeverBePromoted(t *testing.T) {
	s := New()
	for name, tc := range map[string]struct{ kind, value string }{
		"a shell line":      {automode.KindRule, `{"pattern":["git push"]}`},
		"no justification":  {automode.KindRule, `{"pattern":["rm"],"decision":"forbidden"}`},
		"a host with slash": {automode.KindHost, `{"host":"github.com/x","decision":"allow"}`},
	} {
		if _, err := s.CreateAutoModeProposal(tc.kind, "", tc.value, "", time.Now()); err == nil {
			t.Errorf("%s was recorded", name)
		}
	}
	pending, err := s.ListAutoModeProposals("")
	if err != nil {
		t.Fatalf("list proposals: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("a refused proposal reached the table: %+v", pending)
	}
}

func TestAutoModePromoteAppliesAndClosesTheProposal(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	rule, err := s.CreateAutoModeProposal(
		automode.KindRule, "", ruleValue(t, automode.DecisionPrompt, "", "git", "push"), "", now)
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	host, err := s.CreateAutoModeProposal(
		automode.KindHost, "", hostValue(t, "github.com", automode.HostAllow), "", now)
	if err != nil {
		t.Fatalf("create host: %v", err)
	}

	promoted, cfg, err := s.PromoteAutoModeProposal(rule.ID, now)
	if err != nil {
		t.Fatalf("promote rule: %v", err)
	}
	if promoted.State != automode.StatePromoted || promoted.ResolvedAt.IsZero() {
		t.Errorf("promoted proposal = %+v", promoted)
	}
	if got := userRules(cfg.Rules); len(got) != 1 || got[0].Describe() != "git push" ||
		got[0].Decision != automode.DecisionPrompt {
		t.Fatalf("rules = %v", ruleLines(cfg.Rules))
	}
	if _, cfg, err = s.PromoteAutoModeProposal(host.ID, now); err != nil {
		t.Fatalf("promote host: %v", err)
	}
	if len(cfg.Network.AllowedDomains) != 1 || cfg.Network.AllowedDomains[0] != "github.com" {
		t.Errorf("allowed domains = %v", cfg.Network.AllowedDomains)
	}

	read, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if len(userRules(read.Rules)) != 1 || len(read.Network.AllowedDomains) != 1 {
		t.Fatalf("promoted config did not survive the read: %+v", read)
	}
	pending, err := s.ListAutoModeProposals(automode.StatePending)
	if err != nil {
		t.Fatalf("list proposals: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("promoted proposals are still pending: %+v", pending)
	}
}

func TestPromoteReportedAmendmentRecordsAndAppliesInOneMove(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	proposal, cfg, err := s.PromoteReportedAmendment(
		automode.KindRule, ruleValue(t, automode.DecisionAllow, "", "cargo", "build"),
		"pi session sunny-otter", now)
	if err != nil {
		t.Fatalf("report amendment: %v", err)
	}
	if proposal.State != automode.StatePromoted || proposal.ProposedBy != "pi session sunny-otter" {
		t.Errorf("proposal = %+v", proposal)
	}
	if got := userRules(cfg.Rules); len(got) != 1 || got[0].Describe() != "cargo build" {
		t.Fatalf("rules = %v", ruleLines(cfg.Rules))
	}
	if _, _, err := s.PromoteReportedAmendment(
		automode.KindHost, hostValue(t, "crates.io", automode.HostAllow), "pi session sunny-otter", now,
	); err != nil {
		t.Fatalf("report host amendment: %v", err)
	}
	read, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if len(read.Network.AllowedDomains) != 1 || read.Network.AllowedDomains[0] != "crates.io" {
		t.Errorf("allowed domains = %v", read.Network.AllowedDomains)
	}
	pending, err := s.ListAutoModeProposals(automode.StatePending)
	if err != nil {
		t.Fatalf("list proposals: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("a reported amendment left something pending: %+v", pending)
	}
	if _, _, err := s.PromoteReportedAmendment(
		automode.KindRule, `{"pattern":[]}`, "pi session sunny-otter", now); err == nil {
		t.Error("an invalid reported amendment was applied")
	}
}

func TestAutoModePromoteIsIdempotentlyRefusedTwice(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	p, err := s.CreateAutoModeProposal(
		automode.KindRule, "", ruleValue(t, automode.DecisionForbidden, "it deletes the tree", "rm", "-rf"), "", now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := s.PromoteAutoModeProposal(p.ID, now); err != nil {
		t.Fatalf("first promote: %v", err)
	}
	if _, _, err := s.PromoteAutoModeProposal(p.ID, now); err == nil {
		t.Fatal("a second promote was accepted")
	}
	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if got := userRules(cfg.Rules); len(got) != 1 {
		t.Fatalf("promoted rules = %v, want one entry after two promotes", ruleLines(cfg.Rules))
	}
}

func TestAutoModeDiscardLeavesTheConfigAlone(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	p, err := s.CreateAutoModeProposal(
		automode.KindRule, "", ruleValue(t, automode.DecisionAllow, "", "curl"), "", now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	discarded, err := s.DiscardAutoModeProposal(p.ID, now)
	if err != nil {
		t.Fatalf("discard: %v", err)
	}
	if discarded.State != automode.StateDiscarded {
		t.Errorf("state = %q", discarded.State)
	}
	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if len(userRules(cfg.Rules)) != 0 {
		t.Fatalf("rules = %v after a discard", ruleLines(cfg.Rules))
	}
	if _, _, err := s.PromoteAutoModeProposal(p.ID, now); err == nil {
		t.Fatal("a discarded proposal was promoted")
	}
}

func TestAutoModeDenialsReadNewestFirst(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	for _, signature := range []string{"bash curl evil.example", "write /etc/hosts", "bash git push --force"} {
		denial := AutoModeDenial{
			SessionID: "session-1", Tool: "bash", Signature: signature,
			Reason: "outside the envelope", Rule: "guardian",
		}
		if _, dropped, err := s.RecordAutoModeDenial(denial, now); err != nil {
			t.Fatalf("record denial: %v", err)
		} else if dropped != 0 {
			t.Fatalf("dropped %d rows well under the %d-row cap", dropped, AutoModeDenialRows)
		}
	}
	denials, err := s.ListAutoModeDenials(2)
	if err != nil {
		t.Fatalf("list denials: %v", err)
	}
	if len(denials) != 2 {
		t.Fatalf("denials = %d, want the limit of 2", len(denials))
	}
	if denials[0].Signature != "bash git push --force" {
		t.Errorf("newest denial = %q", denials[0].Signature)
	}
	if denials[0].Rule != "guardian" {
		t.Errorf("rule = %q, want who decided", denials[0].Rule)
	}
}

func TestAutoModeDenialsTrimToTheRowCap(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	total := AutoModeDenialRows + 3
	droppedTotal := int64(0)
	for i := range total {
		denial := AutoModeDenial{
			SessionID: "session-1", Tool: "bash",
			Signature: fmt.Sprintf("bash echo %d", i), Reason: "outside the envelope",
		}
		_, dropped, err := s.RecordAutoModeDenial(denial, now)
		if err != nil {
			t.Fatalf("record denial %d: %v", i, err)
		}
		droppedTotal += dropped
	}
	if droppedTotal != 3 {
		t.Errorf("dropped %d rows, want the 3 that overflowed the cap", droppedTotal)
	}
	denials, err := s.ListAutoModeDenials(total)
	if err != nil {
		t.Fatalf("list denials: %v", err)
	}
	if len(denials) != AutoModeDenialRows {
		t.Fatalf("kept %d denials, want the %d-row cap", len(denials), AutoModeDenialRows)
	}
	if want := fmt.Sprintf("bash echo %d", total-1); denials[0].Signature != want {
		t.Errorf("newest kept denial = %q, want %q", denials[0].Signature, want)
	}
	if want := fmt.Sprintf("bash echo %d", total-AutoModeDenialRows); denials[len(denials)-1].Signature != want {
		t.Errorf("oldest kept denial = %q, want %q", denials[len(denials)-1].Signature, want)
	}
}

func TestAutoModeDenialConcurrentDeliveryKeepsOneRow(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 18, 10, 0, 0, 123_000_000, time.UTC)
	denial := AutoModeDenial{
		SessionID: "pi-1", Tool: "bash", Signature: "bash: curl https://one.example",
		Reason: "outside the envelope", Rule: "guardian",
	}
	var wg sync.WaitGroup
	ids := make(chan int64, 8)
	for range cap(ids) {
		wg.Go(func() {
			recorded, dropped, err := s.RecordAutoModeDenial(denial, now)
			if err != nil || dropped != 0 {
				t.Errorf("record denial: dropped=%d error=%v", dropped, err)
			}
			ids <- recorded.ID
		})
	}
	wg.Wait()
	close(ids)
	rows, err := s.ListAutoModeDenials(10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("concurrent deliveries produced %+v, error=%v, want one row", rows, err)
	}
	for id := range ids {
		if id != rows[0].ID {
			t.Errorf("delivery returned id %d, want %d", id, rows[0].ID)
		}
	}
	for _, other := range []struct {
		session, action string
		at              time.Time
	}{
		{"pi-2", denial.Signature, now},
		{denial.SessionID, "write /etc/hosts", now},
		{denial.SessionID, denial.Signature, now.Add(time.Millisecond)},
	} {
		distinct := denial
		distinct.SessionID, distinct.Signature = other.session, other.action
		row, _, err := s.RecordAutoModeDenial(distinct, other.at)
		if err != nil || row.ID == rows[0].ID {
			t.Errorf("distinct denial reused the original row: %+v, error=%v", other, err)
		}
	}
}

func TestAutoModeMigrationCreatesItsTables(t *testing.T) {
	s := New()
	for _, table := range []string{"automode_config", "automode_proposals", "automode_denials"} {
		var name string
		err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s is missing: %v", table, err)
		}
	}
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 109`).Scan(&applied); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("migration 109 applied %d times, want once", applied)
	}
}

// plantPre138AutoModeConfig puts back the glob-list shape migration 138 replaced, so a
// re-run of the chain from `from` sees what an installed machine actually holds.
func plantPre138AutoModeConfig(t *testing.T, s *Store, dbPath, environment, allow, hardDeny string, from int) {
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
			t.Fatalf("plant the pre-138 schema (%s): %v", stmt, err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO automode_config
		(id, enabled_default, environment, allow_patterns, hard_deny, models, updated_at)
		VALUES (1, 1, ?, ?, ?, '[]', '2026-09-01T09:00:00Z')`,
		environment, allow, hardDeny); err != nil {
		t.Fatalf("plant the pre-138 row: %v", err)
	}
	if _, err := s.GetAutoModeConfig(); err == nil {
		t.Fatal("the planted schema already reads; this test would pass without the migration")
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= ?`, from); err != nil {
		t.Fatalf("unrecord migration %d: %v", from, err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}
}

func TestMigration138TurnsGlobsIntoRulesAndKeepsTheRest(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	if _, err := s.db.Exec(`INSERT INTO automode_proposals
		(kind, target, value, proposed_by, state, created_at)
		VALUES ('allow', '', 'git status*', 'session-a', 'pending', '2026-09-01T08:00:00Z')`); err != nil {
		t.Fatalf("plant an old proposal: %v", err)
	}
	plantPre138AutoModeConfig(t, s, dbPath, `{"slots":{},"notes":[]}`,
		`["git status*","rm -rf /","gh pr create *"]`,
		`["*curl*","ssh prod","terraform apply *"]`, 138)

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
}

func TestMigration125KeepsTheOldProseAsNotes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	plantPre138AutoModeConfig(t, s, dbPath, `["this laptop is mine","nothing here serves traffic"]`, `[]`, `[]`, 125)

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

func TestAutoModeShippedRulesSurviveAPromotedRow(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	p, err := s.CreateAutoModeProposal(
		automode.KindRule, "", ruleValue(t, automode.DecisionPrompt, "", "ssh", "prod"), "", now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := s.PromoteAutoModeProposal(p.ID, now); err != nil {
		t.Fatalf("promote: %v", err)
	}
	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	shipped := automode.ShippedRules()
	if len(cfg.Rules) != len(shipped)+1 {
		t.Errorf("rules = %v, want the shipped set plus the promoted one", ruleLines(cfg.Rules))
	}
	if got := userRules(cfg.Rules); len(got) != 1 || got[0].Describe() != "ssh prod" {
		t.Errorf("stored rules = %v, want only the promoted one", ruleLines(got))
	}
	var stored string
	if err := s.db.QueryRow(`SELECT rules FROM automode_config WHERE id = 1`).Scan(&stored); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if strings.Contains(stored, "automode") {
		t.Errorf("persisted rules froze a shipped entry into the row: %s", stored)
	}
}

func TestAutoModeProposalDedupesAnIdenticalPendingOne(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	value := ruleValue(t, automode.DecisionAllow, "", "git", "push")
	first, err := s.CreateAutoModeProposal(automode.KindRule, "", value, "session-a", now)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	again, err := s.CreateAutoModeProposal(automode.KindRule, "", value, "session-a", now)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if again.ID != first.ID {
		t.Errorf("second proposal id = %d, want the existing %d", again.ID, first.ID)
	}
	pending, err := s.ListAutoModeProposals(automode.StatePending)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("pending = %v, want one row", pending)
	}
	if _, err := s.DiscardAutoModeProposal(first.ID, now); err != nil {
		t.Fatalf("discard: %v", err)
	}
	third, err := s.CreateAutoModeProposal(automode.KindRule, "", value, "session-a", now)
	if err != nil {
		t.Fatalf("third: %v", err)
	}
	if third.ID == first.ID {
		t.Error("a discarded proposal was reused instead of a new one being recorded")
	}
}

func TestAutoModeProposalKeepsEachAskerSeparate(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	value := ruleValue(t, automode.DecisionAllow, "", "git", "push")
	first, err := s.CreateAutoModeProposal(automode.KindRule, "", value, "session-a", now)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := s.CreateAutoModeProposal(automode.KindRule, "", value, "session-b", now)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("session-b's ask was collapsed onto session-a's row")
	}
	if second.ProposedBy != "session-b" {
		t.Errorf("second proposal credits %q, want session-b", second.ProposedBy)
	}

	if _, _, err := s.PromoteAutoModeProposal(first.ID, now); err != nil {
		t.Fatalf("promote: %v", err)
	}
	pending, err := s.ListAutoModeProposals(automode.StatePending)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %v, want the sibling ask resolved by the promotion", pending)
	}
	promoted, err := s.ListAutoModeProposals(automode.StatePromoted)
	if err != nil {
		t.Fatalf("list promoted: %v", err)
	}
	if len(promoted) != 2 {
		t.Errorf("promoted = %v, want both askers answered", promoted)
	}
}

func TestAutoModeProposalRaceLandsOneRow(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	value := ruleValue(t, automode.DecisionPrompt, "", "ssh", "prod")
	var wg sync.WaitGroup
	ids := make([]int64, 8)
	errs := make([]error, 8)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := s.CreateAutoModeProposal(automode.KindRule, "", value, "session-a", now)
			ids[i], errs[i] = p.ID, err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("proposal %d: %v", i, err)
		}
		if ids[i] != ids[0] {
			t.Errorf("proposal %d got id %d, want the one row %d", i, ids[i], ids[0])
		}
	}
	pending, err := s.ListAutoModeProposals(automode.StatePending)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("pending = %v, want one row", pending)
	}
	if _, err := s.db.Exec(`
		INSERT INTO automode_proposals (kind, target, value, proposed_by, state, created_at)
		VALUES (?, '', ?, ?, ?, ?)`,
		automode.KindRule, value, "session-a", automode.StatePending,
		now.UTC().Format(sortableTimeFormat)); err == nil {
		t.Error("a duplicate pending ask was accepted straight into the table")
	}
}

func TestAutoModeProposalCapNamesTheLimitAndTheAsk(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	for i := 0; i < automode.MaxPendingProposalsPerProposer; i++ {
		if _, err := s.CreateAutoModeProposal(automode.KindRule, "",
			ruleValue(t, automode.DecisionAllow, "", "curl", fmt.Sprintf("https://example.com/%d", i)),
			"session-a", now,
		); err != nil {
			t.Fatalf("proposal %d: %v", i, err)
		}
	}
	last := ruleValue(t, automode.DecisionAllow, "", "curl", "https://example.com/last")
	_, err := s.CreateAutoModeProposal(automode.KindRule, "", last, "session-a", now)
	if err == nil {
		t.Fatal("the proposal past the cap was accepted")
	}
	for _, want := range []string{
		"session-a",
		fmt.Sprintf("%d", automode.MaxPendingProposalsPerProposer),
		"curl https://example.com/last",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("cap error %q does not name %q", err, want)
		}
	}
	if _, err := s.CreateAutoModeProposal(automode.KindRule, "", last, "session-b", now); err != nil {
		t.Errorf("another proposer was capped too: %v", err)
	}
	pending, _ := s.ListAutoModeProposals(automode.StatePending)
	if _, err := s.DiscardAutoModeProposal(pending[0].ID, now); err != nil {
		t.Fatalf("discard: %v", err)
	}
	after := ruleValue(t, automode.DecisionAllow, "", "curl", "https://example.com/after")
	if _, err := s.CreateAutoModeProposal(automode.KindRule, "", after, "session-a", now); err != nil {
		t.Errorf("a freed slot was still refused: %v", err)
	}
}

func TestAutoModeRuleAddAndRemoveRoundTrip(t *testing.T) {
	s := New()
	now := time.Now()

	rule := automode.Rule{Pattern: automode.Tokens("git", "status"), Decision: automode.DecisionAllow}
	if _, err := s.AddAutoModeRule(rule, now); err != nil {
		t.Fatalf("add rule: %v", err)
	}
	cfg, err := s.AddAutoModeRule(automode.Rule{
		Pattern:       automode.Tokens("terraform", "apply"),
		Decision:      automode.DecisionForbidden,
		Justification: "it changes real infrastructure",
	}, now)
	if err != nil {
		t.Fatalf("add forbidden rule: %v", err)
	}
	if got := ruleLines(userRules(cfg.Rules)); len(got) != 2 {
		t.Fatalf("rules = %v, want both", got)
	}

	// Re-adding the same prefix replaces it: a rule holds one decision.
	cfg, err = s.AddAutoModeRule(automode.Rule{
		Pattern: automode.Tokens("git", "status"), Decision: automode.DecisionPrompt,
	}, now)
	if err != nil {
		t.Fatalf("re-add rule: %v", err)
	}
	if got := userRules(cfg.Rules); len(got) != 2 || got[0].Decision != automode.DecisionPrompt {
		t.Fatalf("rules = %v, want the first replaced in place", ruleLines(cfg.Rules))
	}

	cfg, err = s.RemoveAutoModeRule(automode.Tokens("git", "status"), now)
	if err != nil {
		t.Fatalf("remove rule: %v", err)
	}
	if got := userRules(cfg.Rules); len(got) != 1 || got[0].Describe() != "terraform apply" {
		t.Fatalf("rules after the removal = %v", ruleLines(cfg.Rules))
	}
	if _, err := s.RemoveAutoModeRule(automode.Tokens("git", "status"), now); err == nil {
		t.Error("removing a rule that is not there was accepted")
	}
}

func TestAutoModeRuleEditRefusesAShippedRule(t *testing.T) {
	s := New()
	now := time.Now()
	shipped := automode.ShippedRules()[0]
	if _, err := s.RemoveAutoModeRule(shipped.Pattern, now); err == nil {
		t.Error("a shipped rule was removed")
	}
	_, err := s.AddAutoModeRule(automode.Rule{Pattern: shipped.Pattern, Decision: automode.DecisionAllow}, now)
	if err == nil {
		t.Error("a shipped rule was rewritten to allow")
	}
	if err != nil && !strings.Contains(err.Error(), shipped.Describe()) {
		t.Errorf("error does not name the rule: %v", err)
	}
}

func TestAutoModeHostAddAndRemoveRoundTrip(t *testing.T) {
	s := New()
	now := time.Now()
	cfg, err := s.AddAutoModeHost(automode.HostAmendment{Host: "github.com", Decision: automode.HostAllow}, now)
	if err != nil {
		t.Fatalf("add allowed host: %v", err)
	}
	if len(cfg.Network.AllowedDomains) != 1 {
		t.Fatalf("allowed = %v", cfg.Network.AllowedDomains)
	}

	// A host holds one decision: denying it takes it off the allow list.
	cfg, err = s.AddAutoModeHost(automode.HostAmendment{Host: "github.com", Decision: automode.HostDeny}, now)
	if err != nil {
		t.Fatalf("deny the same host: %v", err)
	}
	if len(cfg.Network.AllowedDomains) != 0 {
		t.Errorf("allowed = %v, want the host moved to denied", cfg.Network.AllowedDomains)
	}
	if !strings.Contains(strings.Join(cfg.Network.DeniedDomains, " "), "github.com") {
		t.Errorf("denied = %v, want the host", cfg.Network.DeniedDomains)
	}

	cfg, err = s.RemoveAutoModeHost(automode.HostAmendment{Host: "github.com", Decision: automode.HostDeny}, now)
	if err != nil {
		t.Fatalf("remove denied host: %v", err)
	}
	shipped := automode.ShippedDeniedDomains(config.WSPort())
	if len(cfg.Network.DeniedDomains) != len(shipped) {
		t.Errorf("denied = %v, want only the shipped entries back", cfg.Network.DeniedDomains)
	}
	if _, err := s.RemoveAutoModeHost(
		automode.HostAmendment{Host: "github.com", Decision: automode.HostDeny}, now); err == nil {
		t.Error("removing a host that is not there was accepted")
	}
	if len(shipped) > 0 {
		if _, err := s.RemoveAutoModeHost(
			automode.HostAmendment{Host: shipped[0], Decision: automode.HostDeny}, now); err == nil {
			t.Error("a shipped denied host was removed")
		}
	}
}

func TestSetAutoModePolicyNamesWhatItRefuses(t *testing.T) {
	s := New()
	now := time.Now()
	cfg, err := s.SetAutoModePolicy(automode.PolicyAmendment{ApprovalPolicy: strPtr(automode.PolicyNever)}, now)
	if err != nil {
		t.Fatalf("set approval policy: %v", err)
	}
	if cfg.ApprovalPolicy != automode.PolicyNever || cfg.SandboxMode != automode.SandboxWorkspaceWrite {
		t.Fatalf("policy = %q/%q, want only the approval policy changed", cfg.ApprovalPolicy, cfg.SandboxMode)
	}
	cfg, err = s.SetAutoModePolicy(automode.PolicyAmendment{SandboxMode: strPtr(automode.SandboxReadOnly)}, now)
	if err != nil {
		t.Fatalf("set sandbox mode: %v", err)
	}
	if cfg.ApprovalPolicy != automode.PolicyNever || cfg.SandboxMode != automode.SandboxReadOnly {
		t.Fatalf("policy = %q/%q, want both settings held", cfg.ApprovalPolicy, cfg.SandboxMode)
	}
	if _, err := s.SetAutoModePolicy(automode.PolicyAmendment{}, now); err == nil {
		t.Error("naming neither field was accepted")
	}
	_, err = s.SetAutoModePolicy(automode.PolicyAmendment{ApprovalPolicy: strPtr("yolo")}, now)
	if err == nil || !strings.Contains(err.Error(), automode.PolicyOnRequest) {
		t.Errorf("error does not name the choices: %v", err)
	}
	read, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if read.ApprovalPolicy != automode.PolicyNever || read.SandboxMode != automode.SandboxReadOnly {
		t.Errorf("policy did not survive the read: %q/%q", read.ApprovalPolicy, read.SandboxMode)
	}
}

func strPtr(value string) *string { return &value }

func promoteValue(t *testing.T, s *Store, kind, value string, now time.Time) automode.Config {
	t.Helper()
	proposal, err := s.CreateAutoModeProposal(kind, "", value, "session-a", now)
	if err != nil {
		t.Fatalf("propose %s: %v", kind, err)
	}
	_, cfg, err := s.PromoteAutoModeProposal(proposal.ID, now)
	if err != nil {
		t.Fatalf("promote %s: %v", kind, err)
	}
	return cfg
}

// One test over every kind: a proposal is the only way the config moves, so each kind
// has to reach the config the app promotes it into.
func TestPromotingEveryAmendmentKindMovesTheConfig(t *testing.T) {
	s := New()
	now := time.Now().UTC()

	cfg := promoteValue(t, s, automode.KindRule, ruleValue(t, automode.DecisionAllow, "", "git", "push"), now)
	if got := ruleLines(userRules(cfg.Rules)); len(got) != 1 || got[0] != "allow git push" {
		t.Fatalf("rules = %v after promoting a rule", got)
	}
	cfg = promoteValue(t, s, automode.KindHost, hostValue(t, "crates.io", automode.HostAllow), now)
	if len(cfg.Network.AllowedDomains) != 1 || cfg.Network.AllowedDomains[0] != "crates.io" {
		t.Fatalf("allowed = %v after promoting a host", cfg.Network.AllowedDomains)
	}

	pattern, err := automode.FormatPatternValue(automode.Tokens("git", "push"))
	if err != nil {
		t.Fatalf("format pattern: %v", err)
	}
	cfg = promoteValue(t, s, automode.KindRuleRemove, pattern, now)
	if got := userRules(cfg.Rules); len(got) != 0 {
		t.Fatalf("rules = %v after promoting a removal", ruleLines(got))
	}
	cfg = promoteValue(t, s, automode.KindHostRemove, hostValue(t, "crates.io", automode.HostAllow), now)
	if len(cfg.Network.AllowedDomains) != 0 {
		t.Fatalf("allowed = %v after promoting a host removal", cfg.Network.AllowedDomains)
	}

	policy, err := automode.FormatPolicyValue(automode.PolicyAmendment{
		ApprovalPolicy:    strPtr(automode.PolicyNever),
		AllowLocalBinding: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("format policy: %v", err)
	}
	cfg = promoteValue(t, s, automode.KindPolicy, policy, now)
	if cfg.ApprovalPolicy != automode.PolicyNever || !cfg.Network.AllowLocalBinding {
		t.Fatalf("policy = %q, local binding = %t", cfg.ApprovalPolicy, cfg.Network.AllowLocalBinding)
	}
	if cfg.SandboxMode != automode.SandboxWorkspaceWrite {
		t.Fatalf("sandbox = %q, want the field the amendment did not name held", cfg.SandboxMode)
	}
	read, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if read.ApprovalPolicy != automode.PolicyNever || !read.Network.AllowLocalBinding {
		t.Fatalf("the promoted policy did not survive the read: %+v", read)
	}
}

// A row planted straight into the table skips the check CreateAutoModeProposal makes,
// which is what proves promotion refuses a shipped entry on its own.
func TestPromotingAPlantedShippedAmendmentIsRefused(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	shipped := automode.ShippedRules()[0]
	rule := ruleValue(t, automode.DecisionAllow, "",
		shipped.Pattern[0].Alternatives[0], shipped.Pattern[1].Alternatives[0], shipped.Pattern[2].Alternatives[0])
	pattern, err := automode.FormatPatternValue(shipped.Pattern)
	if err != nil {
		t.Fatalf("format pattern: %v", err)
	}
	values := map[string]string{automode.KindRule: rule, automode.KindRuleRemove: pattern}
	if domains := automode.ShippedDeniedDomains(config.WSPort()); len(domains) > 0 {
		values[automode.KindHostRemove] = hostValue(t, domains[0], automode.HostDeny)
	}
	for kind, value := range values {
		if _, err := s.CreateAutoModeProposal(kind, "", value, "session-a", now); err == nil {
			t.Errorf("a %s proposal over a built-in entry was recorded", kind)
		}
		res, err := s.db.Exec(`
			INSERT INTO automode_proposals (kind, target, value, proposed_by, state, created_at)
			VALUES (?, '', ?, ?, ?, ?)`,
			kind, value, "session-a", automode.StatePending, now.Format(sortableTimeFormat))
		if err != nil {
			t.Fatalf("plant a %s row: %v", kind, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("planted id: %v", err)
		}
		if _, _, err := s.PromoteAutoModeProposal(id, now); err == nil {
			t.Errorf("promoting the planted %s row over a built-in entry succeeded", kind)
		}
	}
	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if len(userRules(cfg.Rules)) != 0 {
		t.Fatalf("rules = %v, want the built-in entries untouched", ruleLines(cfg.Rules))
	}
	if len(automode.StripShippedNetwork(config.WSPort(), cfg.Network).DeniedDomains) != 0 {
		t.Fatalf("denied = %v, want the built-in host untouched", cfg.Network.DeniedDomains)
	}
}

// The stored JSON on an installed machine predates allow_local_binding, and reading it
// must land on the closed half of the switch rather than on nothing at all.
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

// "git push" and "git {push|pull}" are two rules, so a removal that carried only
// the first alternative of each token would take away the wrong one.
func TestRemovingARuleWithAlternativesLeavesItsLiteralNamesake(t *testing.T) {
	s := New()
	now := time.Now()
	literal := automode.Rule{Pattern: automode.Tokens("git", "push"), Decision: automode.DecisionAllow}
	alternatives := automode.Rule{
		Pattern: []automode.PatternToken{
			automode.Token("git"), automode.Token("push", "pull"),
		},
		Decision: automode.DecisionPrompt,
	}
	for _, rule := range []automode.Rule{literal, alternatives} {
		if _, err := s.AddAutoModeRule(rule, now); err != nil {
			t.Fatalf("add %s: %v", rule.Describe(), err)
		}
	}
	cfg, err := s.RemoveAutoModeRule(alternatives.Pattern, now)
	if err != nil {
		t.Fatalf("remove the alternatives rule: %v", err)
	}
	got := ruleLines(userRules(cfg.Rules))
	if len(got) != 1 || got[0] != "allow git push" {
		t.Errorf("rules = %v, want only the literal one left", got)
	}
}

func TestDismissingALegacyPatternDropsOnlyThatEntry(t *testing.T) {
	s := New()
	now := time.Now()
	if _, err := s.mutateAutoModeConfig(now, func(cfg *automode.Config) error {
		cfg.LegacyPatterns = []string{"git status*", "*curl*", "ssh prod*"}
		return nil
	}); err != nil {
		t.Fatalf("seed the legacy patterns: %v", err)
	}
	cfg, err := s.DismissAutoModeLegacyPattern("*curl*", now)
	if err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if strings.Join(cfg.LegacyPatterns, "; ") != "git status*; ssh prod*" {
		t.Errorf("legacy patterns = %v, want the other two", cfg.LegacyPatterns)
	}
	stored, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if strings.Join(stored.LegacyPatterns, "; ") != "git status*; ssh prod*" {
		t.Errorf("stored legacy patterns = %v, want the dismissal persisted", stored.LegacyPatterns)
	}
	if _, err := s.DismissAutoModeLegacyPattern("*curl*", now); err == nil {
		t.Error("dismissing a pattern that is not on the list was accepted")
	}
}

func boolPtr(value bool) *bool { return &value }
