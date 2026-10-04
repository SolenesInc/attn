package store

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
)

func TestSessionCostReadsLedgerKeysWrittenBeforePurposesExisted(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Add(&protocol.Session{ID: "legacy", Label: "legacy"})

	legacy := `{"initialized":true,"ledger":{"claude-opus-4-8":{"input_tokens":10,"output_tokens":2}}}`
	if _, err := s.db.Exec("UPDATE sessions SET session_cost_json = ? WHERE id = ?", legacy, "legacy"); err != nil {
		t.Fatal(err)
	}
	state, err := s.SessionCost("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Ledger[sessioncost.AgentKey("claude-opus-4-8")]; got.InputTokens != 10 || got.OutputTokens != 2 {
		t.Fatalf("legacy ledger key did not decode as the agent's own: %+v", state.Ledger)
	}

	observation := SessionCostObservation{
		ObservationID: "claude:msg-2", Model: "claude-opus-4-8",
		Usage: sessioncost.Usage{InputTokens: 5, OutputTokens: 1},
	}
	if changed, err := s.ApplySessionCostObservations("legacy", "cursor-1", []SessionCostObservation{observation}); err != nil || !changed {
		t.Fatalf("apply changed=%v err=%v", changed, err)
	}
	state, _ = s.SessionCost("legacy")
	if len(state.Ledger) != 1 {
		t.Fatalf("new observation did not merge into the legacy row: %+v", state.Ledger)
	}
	if got := state.Ledger[sessioncost.AgentKey("claude-opus-4-8")]; got.InputTokens != 15 || got.OutputTokens != 3 {
		t.Fatalf("merged row = %+v", got)
	}
}

func TestMigration152FilesStoredLongContextObservationsUnderTheirTier(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(dbPath, 167)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`INSERT INTO sessions (id, label, directory, state, state_since, state_updated_at, last_seen) VALUES ('sol', 'sol', '', 'idle', '', '', '')`); err != nil {
		t.Fatal(err)
	}
	legacy := `{"initialized":true,
		"ledger":{"agent|gpt-6-sol":{"input_tokens":144001,"output_tokens":20000,"cache_read_input_tokens":400000}},
		"observations":{
			"codex:1":{"observation_id":"codex:1","model":"gpt-6-sol","purpose":"agent","usage":{"input_tokens":72000,"output_tokens":10000,"cache_read_input_tokens":200000}},
			"codex:2":{"observation_id":"codex:2","model":"gpt-6-sol","purpose":"agent","usage":{"input_tokens":72001,"output_tokens":10000,"cache_read_input_tokens":200000}}}}`
	if _, err := s.db.Exec("UPDATE sessions SET session_cost_json = ? WHERE id = ?", legacy, "sol"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.Exec("DELETE FROM schema_migrations WHERE version >= 152"); err != nil {
		t.Fatal(err)
	}
	if err := migrateDBThrough(s.db, dbPath, 167); err != nil {
		t.Fatal(err)
	}

	revised := SessionCostObservation{
		ObservationID: "codex:2", Model: "gpt-6-sol", Purpose: sessioncost.PurposeAgent,
		Usage: sessioncost.Usage{InputTokens: 72_001, CacheReadInputTokens: 200_000, OutputTokens: 20_000},
	}
	if _, err := s.ApplySessionCostObservations("sol", "cursor-1", []SessionCostObservation{revised}); err != nil {
		t.Fatal(err)
	}
	state, err := s.SessionCost("sol")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Ledger) != 2 {
		t.Fatalf("ledger = %+v, want a standard and a long-context row", state.Ledger)
	}
	summary := sessioncost.Summarize(state.Ledger, nil)
	standardUSD := (72_000*2 + 200_000*0.2 + 10_000*10) / 1e6
	longContextUSD := (72_001*4 + 200_000*0.4 + 20_000*15) / 1e6
	if !summary.Valid || summary.CostUSD == nil || math.Abs(*summary.CostUSD-(standardUSD+longContextUSD)) > 1e-12 {
		t.Fatalf("summary = %+v, want cost %.6f", summary, standardUSD+longContextUSD)
	}
}

func TestMigration163FilesGPT61SolObservationsUnderTheirTier(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(dbPath, 167)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`INSERT INTO sessions (id, label, directory, state, state_since, state_updated_at, last_seen) VALUES ('sol', 'sol', '', 'idle', '', '', '')`); err != nil {
		t.Fatal(err)
	}
	legacy := `{"initialized":true,
		"ledger":{"agent|gpt-6.1-sol":{"input_tokens":144001,"output_tokens":20000,"cache_read_input_tokens":400000}},
		"observations":{
			"codex:1":{"observation_id":"codex:1","model":"gpt-6.1-sol","purpose":"agent","usage":{"input_tokens":72000,"output_tokens":10000,"cache_read_input_tokens":200000}},
			"codex:2":{"observation_id":"codex:2","model":"gpt-6.1-sol","purpose":"agent","usage":{"input_tokens":72001,"output_tokens":10000,"cache_read_input_tokens":200000}}}}`
	if _, err := s.db.Exec("UPDATE sessions SET session_cost_json = ? WHERE id = ?", legacy, "sol"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DELETE FROM schema_migrations WHERE version >= 163"); err != nil {
		t.Fatal(err)
	}
	if err := migrateDBThrough(s.db, dbPath, 167); err != nil {
		t.Fatal(err)
	}

	revised := SessionCostObservation{
		ObservationID: "codex:2", Model: "gpt-6.1-sol", Purpose: sessioncost.PurposeAgent,
		Usage: sessioncost.Usage{InputTokens: 72_001, CacheReadInputTokens: 200_000, OutputTokens: 20_000},
	}
	if _, err := s.ApplySessionCostObservations("sol", "cursor-1", []SessionCostObservation{revised}); err != nil {
		t.Fatal(err)
	}
	state, err := s.SessionCost("sol")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Ledger) != 2 {
		t.Fatalf("ledger = %+v, want a standard and a long-context row", state.Ledger)
	}
	summary := sessioncost.Summarize(state.Ledger, nil)
	standardUSD := (72_000*2 + 200_000*0.1 + 10_000*10) / 1e6
	longContextUSD := (72_001*4 + 200_000*0.2 + 20_000*15) / 1e6
	if !summary.Valid || summary.CostUSD == nil || math.Abs(*summary.CostUSD-(standardUSD+longContextUSD)) > 1e-12 {
		t.Fatalf("summary = %+v, want cost %.6f", summary, standardUSD+longContextUSD)
	}
}

func TestLegacyCostMigrationsKeepTheirStoredTierRules(t *testing.T) {
	cases := []struct {
		name, model, purpose, usage, want string
		fast                              bool
	}{
		{"threshold", "gpt-6-sol", "agent", `"input_tokens":272000`, "agent|gpt-6-sol", false},
		{"input", "gpt-6-sol", "agent", `"input_tokens":272001`, "agent|gpt-6-sol|long-context", false},
		{"alias and cache read", "codex-auto-review", "agent", `"cache_read_input_tokens":272001`, "agent|codex-auto-review|long-context", false},
		{"five minute cache", "gpt-5.5", "agent", `"cache_write_5m_input_tokens":272001`, "agent|gpt-5.5|long-context", false},
		{"one hour cache", "gpt-6-astra", "agent", `"cache_write_1h_input_tokens":272001`, "agent|gpt-6-astra|long-context", false},
		{"unclassified cache", "gpt-6-luna", "agent", `"unclassified_cache_write_tokens":272001`, "agent|gpt-6-luna|long-context", false},
		{"unknown model", "future-model", "agent", `"input_tokens":272001`, "agent|future-model", false},
		{"fast sol", "gpt-6.1-sol", "guardian", `"input_tokens":272001`, "guardian|gpt-6.1-sol|long-context|fast", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "attn.db")
			s, err := newStoreAtVersion(dbPath, 167)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			s.Add(&protocol.Session{ID: "fixture", Label: "fixture"})
			raw := fmt.Sprintf(`{"initialized":true,"ledger":{"%s|%s":{%s}},"observations":{"request":{"observation_id":"request","model":%q,"purpose":%q,"fast_mode":%t,"usage":{%s}}}}`, c.purpose, c.model, c.usage, c.model, c.purpose, c.fast, c.usage)
			if _, err := s.db.Exec("UPDATE sessions SET session_cost_json = ? WHERE id = 'fixture'", raw); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec("DELETE FROM schema_migrations WHERE version >= 152"); err != nil {
				t.Fatal(err)
			}
			if err := migrateDBThrough(s.db, dbPath, 167); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow("SELECT session_cost_json FROM sessions WHERE id = 'fixture'").Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var result struct {
				Ledger map[string]json.RawMessage `json:"ledger"`
			}
			if err := json.Unmarshal([]byte(raw), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Ledger) != 1 || result.Ledger[c.want] == nil {
				t.Fatalf("ledger = %s, want only %s", raw, c.want)
			}
		})
	}
}
