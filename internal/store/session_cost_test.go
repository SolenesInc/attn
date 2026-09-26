package store

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
)

func TestSessionCostSeparatesGuardianTrafficFromTheAgentsOwn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Add(&protocol.Session{ID: "pi", Label: "pi"})

	agent := SessionCostObservation{
		ObservationID: "pi:a4e94c7b", Model: "deepseek-v4-flash", Purpose: sessioncost.PurposeAgent,
		Usage: sessioncost.Usage{InputTokens: 5379, OutputTokens: 232, ReportedCostUSD: 0.0013365},
	}
	guardian := SessionCostObservation{
		ObservationID: "pi:c1f0a2b7", Model: "deepseek-v4-flash", Purpose: sessioncost.PurposeGuardian,
		Usage: sessioncost.Usage{InputTokens: 812, OutputTokens: 64, ReportedCostUSD: 0.00022088},
	}
	if changed, err := s.ApplySessionCostObservations("pi", "cursor-1", []SessionCostObservation{agent, guardian}); err != nil || !changed {
		t.Fatalf("apply changed=%v err=%v", changed, err)
	}
	state, err := s.SessionCost("pi")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Ledger) != 2 {
		t.Fatalf("ledger = %+v, want the model split by purpose", state.Ledger)
	}
	if state.Ledger[sessioncost.AgentKey("deepseek-v4-flash")] != agent.Usage {
		t.Fatalf("agent row = %+v", state.Ledger[sessioncost.AgentKey("deepseek-v4-flash")])
	}
	if state.Ledger[sessioncost.GuardianKey("deepseek-v4-flash")] != guardian.Usage {
		t.Fatalf("guardian row = %+v", state.Ledger[sessioncost.GuardianKey("deepseek-v4-flash")])
	}

	revised := guardian
	revised.Usage.OutputTokens = 91
	if changed, err := s.ApplySessionCostObservations("pi", "cursor-2", []SessionCostObservation{revised}); err != nil || !changed {
		t.Fatalf("revision changed=%v err=%v", changed, err)
	}
	state, _ = s.SessionCost("pi")
	if got := state.Ledger[sessioncost.GuardianKey("deepseek-v4-flash")].OutputTokens; got != 91 {
		t.Fatalf("guardian output after revision = %d, want 91", got)
	}
	if state.Ledger[sessioncost.AgentKey("deepseek-v4-flash")] != agent.Usage {
		t.Fatalf("agent row moved when the guardian row was revised: %+v", state.Ledger)
	}
}

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
	s, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Add(&protocol.Session{ID: "sol", Label: "sol"})
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
	if err := migrateDB(s.db, dbPath); err != nil {
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
