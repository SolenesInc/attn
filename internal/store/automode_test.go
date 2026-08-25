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

func userHardDeny(resolved []string) []string {
	return automode.StripShippedHardDeny(config.WSPort(), resolved)
}

func TestAutoModeConfigDefaultsOnAFreshDatabase(t *testing.T) {
	s := New()
	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if len(cfg.Models) != 0 {
		t.Errorf("models = %v on a fresh database, want none until the user names one", cfg.Models)
	}
	if !cfg.EnabledDefault {
		t.Error("enabled_default = false on a fresh database, want true")
	}
	if filled, _ := cfg.Environment.Filled(); len(cfg.Allow) != 0 || filled != 0 {
		t.Errorf("fresh config is not empty: %+v", cfg)
	}
	if diff := len(cfg.HardDeny) - len(automode.ShippedHardDeny(config.WSPort())); diff != 0 {
		t.Errorf("hard deny = %v, want exactly the shipped denies", cfg.HardDeny)
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
	// Editing the environment must not disturb the models a promote set.
	if len(read.Models) != 0 {
		t.Errorf("models drifted to %v", read.Models)
	}
}

func TestAutoModeEnvironmentSlotRefusesWhatTheSchemaDoesNotHave(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	if _, err := s.SetAutoModeEnvironmentSlot("intranet", []string{"acme.corp"}, now); err == nil {
		t.Fatal("a slot the rules never read was accepted; nothing would ever look it up")
	}
	if _, err := s.SetAutoModeEnvironmentSlot("repo_visibility", []string{"secret"}, now); err == nil {
		t.Fatal("repo_visibility took a value outside its choices")
	}
	cfg, err := s.SetAutoModeEnvironmentSlot("repo_visibility", []string{"private"}, now)
	if err != nil {
		t.Fatalf("set visibility: %v", err)
	}
	if got := cfg.Environment.Slots["repo_visibility"]; len(got) != 1 || got[0] != "private" {
		t.Errorf("repo_visibility = %v", got)
	}
	cleared, err := s.SetAutoModeEnvironmentSlot("repo_visibility", nil, now)
	if err != nil {
		t.Fatalf("clear visibility: %v", err)
	}
	if _, ok := cleared.Environment.Slots["repo_visibility"]; ok {
		t.Error("clearing left the slot behind; an unset slot has to read as unset")
	}
}

func TestAutoModeProposalDoesNotChangeTheConfig(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	before, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if _, err := s.CreateAutoModeProposal(automode.KindAllow, "", "git push origin*", "session-1", now); err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	if _, err := s.CreateAutoModeProposal(automode.KindModel, automode.TargetModels, "opencode-go/other-model", "", now); err != nil {
		t.Fatalf("create model proposal: %v", err)
	}
	after, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if len(after.Allow) != 0 {
		t.Errorf("allow list changed to %v", after.Allow)
	}
	if strings.Join(after.Models, ",") != strings.Join(before.Models, ",") {
		t.Errorf("models changed to %v", after.Models)
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

func TestAutoModeCreateProposalRefusesABroadAllow(t *testing.T) {
	s := New()
	if _, err := s.CreateAutoModeProposal(automode.KindAllow, "", "*", "", time.Now()); err == nil {
		t.Fatal("a broad allow proposal was recorded")
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
	allow, err := s.CreateAutoModeProposal(automode.KindAllow, "", "git push origin*", "", now)
	if err != nil {
		t.Fatalf("create allow: %v", err)
	}
	model, err := s.CreateAutoModeProposal(automode.KindModel, automode.TargetModels, "opencode-go/kimi-k3", "", now)
	if err != nil {
		t.Fatalf("create model: %v", err)
	}

	promoted, cfg, err := s.PromoteAutoModeProposal(allow.ID, now)
	if err != nil {
		t.Fatalf("promote allow: %v", err)
	}
	if promoted.State != automode.StatePromoted || promoted.ResolvedAt.IsZero() {
		t.Errorf("promoted proposal = %+v", promoted)
	}
	if len(cfg.Allow) != 1 || cfg.Allow[0] != "git push origin*" {
		t.Fatalf("allow list = %v", cfg.Allow)
	}
	if _, cfg, err = s.PromoteAutoModeProposal(model.ID, now); err != nil {
		t.Fatalf("promote model: %v", err)
	}
	if len(cfg.Models) != 1 || cfg.Models[0] != "opencode-go/kimi-k3" {
		t.Errorf("models = %v", cfg.Models)
	}

	read, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if len(read.Allow) != 1 || len(read.Models) != 1 || read.Models[0] != "opencode-go/kimi-k3" {
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

func TestAutoModePromoteIsIdempotentlyRefusedTwice(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	p, err := s.CreateAutoModeProposal(automode.KindDeny, "", "rm -rf /*", "", now)
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
	if got := userHardDeny(cfg.HardDeny); len(got) != 1 {
		t.Fatalf("promoted hard deny = %v, want one entry after two promotes", got)
	}
}

func TestAutoModeDiscardLeavesTheConfigAlone(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	p, err := s.CreateAutoModeProposal(automode.KindAllow, "", "curl https://example.com*", "", now)
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
	if len(cfg.Allow) != 0 {
		t.Fatalf("allow list = %v after a discard", cfg.Allow)
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
			Reason: "outside the envelope", Rule: "classifier-2a",
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
	if denials[0].Rule != "classifier-2a" {
		t.Errorf("rule = %q, want the layer that decided", denials[0].Rule)
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

func TestMigration122FoldsTheLayerModelsIntoOneChain(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	for _, stmt := range []string{
		`ALTER TABLE automode_config DROP COLUMN models`,
		`ALTER TABLE automode_config ADD COLUMN classifier_models TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE automode_config ADD COLUMN escalation_models TEXT NOT NULL DEFAULT '[]'`,
		`INSERT INTO automode_config (id, enabled_default, environment, allow_patterns, hard_deny,
		    classifier_models, escalation_models, updated_at)
		 VALUES (1, 1, '[]', '[]', '[]', '["vendor/small","vendor/shared"]',
		         '["vendor/shared","vendor/big"]', '2026-08-22T09:00:00Z')`,
		`INSERT INTO automode_proposals (kind, target, value, proposed_by, state, created_at)
		 VALUES ('model', 'models', 'vendor/small', 'test', 'promoted', '2026-08-22T08:00:00Z')`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("plant the pre-122 schema (%s): %v", stmt, err)
		}
	}
	if _, err := s.GetAutoModeConfig(); err == nil {
		t.Fatal("the planted schema already has the folded column; this test would pass without the migration")
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 122`); err != nil {
		t.Fatalf("unrecord migration 122: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}

	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config after the migration: %v", err)
	}
	want := "vendor/small,vendor/shared,vendor/big"
	if strings.Join(cfg.Models, ",") != want {
		t.Errorf("models = %v, want %s", cfg.Models, want)
	}
	for _, column := range []string{"classifier_models", "escalation_models"} {
		var rows int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('automode_config') WHERE name = ?`, column).Scan(&rows); err != nil {
			t.Fatalf("read table info: %v", err)
		}
		if rows != 0 {
			t.Errorf("column %s survived the migration; two spellings of one setting is how one goes stale", column)
		}
	}
}

func TestMigration123KeepsTheOldProseAsNotes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	if _, err := s.db.Exec(`INSERT INTO automode_config
		(id, enabled_default, environment, allow_patterns, hard_deny, models, updated_at)
		VALUES (1, 1, '["this laptop is mine","nothing here serves traffic"]', '[]', '[]', '[]', '2026-08-23T09:00:00Z')`); err != nil {
		t.Fatalf("plant the pre-123 row: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 123`); err != nil {
		t.Fatalf("unrecord migration 123: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}

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

func TestMigration122DropsModelsNobodyPromoted(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	for _, stmt := range []string{
		`ALTER TABLE automode_config DROP COLUMN models`,
		`ALTER TABLE automode_config ADD COLUMN classifier_models TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE automode_config ADD COLUMN escalation_models TEXT NOT NULL DEFAULT '[]'`,
		`INSERT INTO automode_config (id, enabled_default, environment, allow_patterns, hard_deny,
		    classifier_models, escalation_models, updated_at)
		 VALUES (1, 1, '[]', '["Bash(ls:*)"]', '[]', '["opencode-go/glm-5.3"]',
		         '["opencode-go/qwen3.8-max"]', '2026-08-22T09:00:00Z')`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("plant the pre-122 schema (%s): %v", stmt, err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 122`); err != nil {
		t.Fatalf("unrecord migration 122: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}

	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config after the migration: %v", err)
	}
	if len(cfg.Models) != 0 {
		t.Errorf("models = %v, want none: nobody promoted those", cfg.Models)
	}
	if len(cfg.Allow) != 1 || cfg.Allow[0] != "Bash(ls:*)" {
		t.Errorf("allow = %v, want the pattern the machine actually saved", cfg.Allow)
	}
}

// Migration 114 turns the single promoted model into its layer's one-entry list.
// A machine that promoted one must launch on it, not on the shipped default.
func TestMigration114CarriesAPromotedModelIntoItsLayersList(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	for _, stmt := range []string{
		`ALTER TABLE automode_config DROP COLUMN models`,
		`ALTER TABLE automode_config ADD COLUMN classifier_model TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE automode_config ADD COLUMN escalation_model TEXT NOT NULL DEFAULT ''`,
		`INSERT INTO automode_config (id, enabled_default, environment, allow_patterns, hard_deny,
		    classifier_model, escalation_model, updated_at)
		 VALUES (1, 1, '[]', '[]', '[]', 'vendor/picked', '', '2026-08-17T09:00:00Z')`,
		`INSERT INTO automode_proposals (kind, target, value, proposed_by, state, created_at)
		 VALUES ('model', 'models', 'vendor/picked', 'test', 'promoted', '2026-08-17T08:00:00Z')`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("plant the pre-114 schema (%s): %v", stmt, err)
		}
	}

	if _, err := s.GetAutoModeConfig(); err == nil {
		t.Fatal("the planted schema already has the lists; this test would pass without the migration")
	}

	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 114`); err != nil {
		t.Fatalf("unrecord migration 114: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}

	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config after the migration: %v", err)
	}
	if len(cfg.Models) != 1 || cfg.Models[0] != "vendor/picked" {
		t.Errorf("models = %v, want the promoted model carried over", cfg.Models)
	}
	for _, column := range []string{"classifier_model", "escalation_model"} {
		var rows int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('automode_config') WHERE name = ?`, column).Scan(&rows); err != nil {
			t.Fatalf("read table info: %v", err)
		}
		if rows != 0 {
			t.Errorf("column %s survived the migration; two spellings of one setting is how one goes stale", column)
		}
	}
}

func TestAutoModeShippedHardDeniesSurviveAPromotedRow(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	p, err := s.CreateAutoModeProposal(automode.KindDeny, "", "ssh prod*", "", now)
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
	for _, want := range automode.ShippedHardDeny(config.WSPort()) {
		if !containsString(cfg.HardDeny, want) {
			t.Errorf("hard deny %v is missing the shipped entry %q", cfg.HardDeny, want)
		}
	}
	if got := userHardDeny(cfg.HardDeny); len(got) != 1 || got[0] != "ssh prod*" {
		t.Errorf("stored hard deny = %v, want only the promoted pattern", got)
	}
	var stored string
	if err := s.db.QueryRow(`SELECT hard_deny FROM automode_config WHERE id = 1`).Scan(&stored); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if stored != `["ssh prod*"]` {
		t.Errorf("persisted hard_deny = %s, want only the promoted pattern", stored)
	}
}

func TestAutoModeProposalDedupesAnIdenticalPendingOne(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	first, err := s.CreateAutoModeProposal(automode.KindAllow, "", "git push origin*", "session-a", now)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	again, err := s.CreateAutoModeProposal(automode.KindAllow, "", "git push origin*", "session-a", now)
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
	third, err := s.CreateAutoModeProposal(automode.KindAllow, "", "git push origin*", "session-a", now)
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
	first, err := s.CreateAutoModeProposal(automode.KindAllow, "", "git push origin*", "session-a", now)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := s.CreateAutoModeProposal(automode.KindAllow, "", "git push origin*", "session-b", now)
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
	var wg sync.WaitGroup
	ids := make([]int64, 8)
	errs := make([]error, 8)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := s.CreateAutoModeProposal(automode.KindDeny, "", "ssh prod*", "session-a", now)
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
		automode.KindDeny, "ssh prod*", "session-a", automode.StatePending,
		now.UTC().Format(sortableTimeFormat)); err == nil {
		t.Error("a duplicate pending ask was accepted straight into the table")
	}
}

func TestAutoModeProposalCapNamesTheLimitAndTheAsk(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	for i := 0; i < automode.MaxPendingProposalsPerProposer; i++ {
		if _, err := s.CreateAutoModeProposal(
			automode.KindAllow, "", fmt.Sprintf("curl https://example.com/%d*", i), "session-a", now,
		); err != nil {
			t.Fatalf("proposal %d: %v", i, err)
		}
	}
	_, err := s.CreateAutoModeProposal(automode.KindAllow, "", "curl https://example.com/last*", "session-a", now)
	if err == nil {
		t.Fatal("the proposal past the cap was accepted")
	}
	for _, want := range []string{
		"session-a",
		fmt.Sprintf("%d", automode.MaxPendingProposalsPerProposer),
		"curl https://example.com/last*",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("cap error %q does not name %q", err, want)
		}
	}
	if _, err := s.CreateAutoModeProposal(automode.KindAllow, "", "curl https://example.com/last*", "session-b", now); err != nil {
		t.Errorf("another proposer was capped too: %v", err)
	}
	pending, _ := s.ListAutoModeProposals(automode.StatePending)
	if _, err := s.DiscardAutoModeProposal(pending[0].ID, now); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if _, err := s.CreateAutoModeProposal(automode.KindAllow, "", "curl https://example.com/after*", "session-a", now); err != nil {
		t.Errorf("a freed slot was still refused: %v", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestAutoModePatternAddAndRemoveRoundTrip(t *testing.T) {
	s := New()
	now := time.Now()

	if _, err := s.AddAutoModePattern(automode.ListAllow, "git status*", now); err != nil {
		t.Fatalf("add allow: %v", err)
	}
	if _, err := s.AddAutoModePattern(automode.ListHardDeny, "*terraform apply*", now); err != nil {
		t.Fatalf("add hard deny: %v", err)
	}

	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if len(cfg.Allow) != 1 || cfg.Allow[0] != "git status*" {
		t.Fatalf("allow = %v", cfg.Allow)
	}
	if got := userHardDeny(cfg.HardDeny); len(got) != 1 || got[0] != "*terraform apply*" {
		t.Fatalf("stored hard deny = %v", got)
	}

	if _, err := s.RemoveAutoModePattern(automode.ListAllow, "git status*", now); err != nil {
		t.Fatalf("remove allow: %v", err)
	}
	if _, err := s.RemoveAutoModePattern(automode.ListHardDeny, "*terraform apply*", now); err != nil {
		t.Fatalf("remove hard deny: %v", err)
	}
	cfg, err = s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("re-read config: %v", err)
	}
	if len(cfg.Allow) != 0 || len(userHardDeny(cfg.HardDeny)) != 0 {
		t.Fatalf("removal left something behind: allow=%v hard_deny=%v", cfg.Allow, cfg.HardDeny)
	}
	if len(cfg.HardDeny) != len(automode.ShippedHardDeny(config.WSPort())) {
		t.Fatalf("resolved hard deny = %v, want exactly the shipped denies", cfg.HardDeny)
	}
}

func TestAutoModePatternEditNeverStoresAShippedHardDeny(t *testing.T) {
	s := New()
	now := time.Now()

	if _, err := s.AddAutoModePattern(automode.ListHardDeny, "*terraform apply*", now); err != nil {
		t.Fatalf("add hard deny: %v", err)
	}
	var stored string
	if err := s.db.QueryRow(`SELECT hard_deny FROM automode_config WHERE id = 1`).Scan(&stored); err != nil {
		t.Fatalf("read the row: %v", err)
	}
	for _, shipped := range automode.ShippedHardDeny(config.WSPort()) {
		if strings.Contains(stored, shipped) {
			t.Fatalf("the row persisted the shipped deny %q: %s", shipped, stored)
		}
	}
}

func TestAutoModeRemoveRefusesAShippedHardDeny(t *testing.T) {
	s := New()
	shipped := automode.ShippedHardDeny(config.WSPort())[0]

	_, err := s.RemoveAutoModePattern(automode.ListHardDeny, shipped, time.Now())
	if err == nil {
		t.Fatal("removing a shipped hard deny succeeded")
	}
	if !strings.Contains(err.Error(), "built-in") || !strings.Contains(err.Error(), shipped) {
		t.Fatalf("refusal does not name what it refused: %v", err)
	}
}

func TestAutoModeAddRefusesABroadAllowAndAnEmptyDeny(t *testing.T) {
	s := New()
	now := time.Now()

	_, err := s.AddAutoModePattern(automode.ListAllow, "* *", now)
	if err == nil {
		t.Fatal("a broad allow was accepted")
	}
	if !strings.Contains(err.Error(), "must name something") {
		t.Fatalf("broad allow refusal = %v", err)
	}
	if _, err := s.AddAutoModePattern(automode.ListHardDeny, "*", now); err != nil {
		t.Fatalf("a broad hard deny was refused: %v", err)
	}
	if _, err := s.AddAutoModePattern(automode.ListHardDeny, "   ", now); err == nil {
		t.Fatal("an empty deny was accepted")
	}
}

func TestAutoModePatternEditNamesADuplicateAndAMiss(t *testing.T) {
	s := New()
	now := time.Now()

	if _, err := s.AddAutoModePattern(automode.ListAllow, "git status*", now); err != nil {
		t.Fatalf("add allow: %v", err)
	}
	_, err := s.AddAutoModePattern(automode.ListAllow, "git status*", now)
	if err == nil || !strings.Contains(err.Error(), "already in the allow list") {
		t.Fatalf("duplicate add = %v", err)
	}
	_, err = s.RemoveAutoModePattern(automode.ListAllow, "never added", now)
	if err == nil || !strings.Contains(err.Error(), "not in the allow list") {
		t.Fatalf("missing removal = %v", err)
	}
	_, err = s.AddAutoModePattern("models", "x", now)
	if err == nil || !strings.Contains(err.Error(), "unknown pattern list") {
		t.Fatalf("unknown list = %v", err)
	}
}

func TestAutoModeDirectEditAndPromotionShareTheList(t *testing.T) {
	s := New()
	now := time.Now()

	if _, err := s.AddAutoModePattern(automode.ListAllow, "git status*", now); err != nil {
		t.Fatalf("add allow: %v", err)
	}
	proposal, err := s.CreateAutoModeProposal(automode.KindAllow, "", "git push origin*", "session-a", now)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	_, cfg, err := s.PromoteAutoModeProposal(proposal.ID, now)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if len(cfg.Allow) != 2 || cfg.Allow[0] != "git status*" || cfg.Allow[1] != "git push origin*" {
		t.Fatalf("allow after promotion = %v", cfg.Allow)
	}
	cfg, err = s.RemoveAutoModePattern(automode.ListAllow, "git status*", now)
	if err != nil {
		t.Fatalf("remove after promotion: %v", err)
	}
	if len(cfg.Allow) != 1 || cfg.Allow[0] != "git push origin*" {
		t.Fatalf("allow after removal = %v", cfg.Allow)
	}
}
