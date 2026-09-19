package store

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
)

func TestSessionCostObservationsPersistAndReplaceAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s.Add(&protocol.Session{ID: "cost", Label: "cost"})
	first := SessionCostObservation{
		ObservationID: "claude:msg-1", Model: "claude-opus-4-8",
		Usage: sessioncost.Usage{InputTokens: 2, CacheWrite5mInputTokens: 100, OutputTokens: 3},
	}
	if changed, err := s.ApplySessionCostObservations("cost", "cursor-1", []SessionCostObservation{first}); err != nil || !changed {
		t.Fatalf("first apply changed=%v err=%v", changed, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state, err := s.SessionCost("cost")
	if err != nil {
		t.Fatal(err)
	}
	if state.Cursor != "cursor-1" || state.Ledger[sessioncost.AgentKey("claude-opus-4-8")] != first.Usage {
		t.Fatalf("reopened state = %+v", state)
	}

	revised := first
	revised.Usage.OutputTokens = 8
	if changed, err := s.ApplySessionCostObservations("cost", "cursor-2", []SessionCostObservation{revised}); err != nil || !changed {
		t.Fatalf("revision changed=%v err=%v", changed, err)
	}
	state, _ = s.SessionCost("cost")
	if got := state.Ledger[sessioncost.AgentKey("claude-opus-4-8")].OutputTokens; got != 8 {
		t.Fatalf("output after absolute revision = %d, want 8 (not 11)", got)
	}
	if changed, err := s.ApplySessionCostObservations("cost", "cursor-3", []SessionCostObservation{revised}); err != nil || changed {
		t.Fatalf("duplicate apply changed=%v err=%v", changed, err)
	}
	state, _ = s.SessionCost("cost")
	if state.Cursor != "cursor-3" {
		t.Fatalf("cursor = %q, want cursor-3", state.Cursor)
	}
	if changed, err := s.MarkSessionCostUsageUnavailable("cost", "cursor-4"); err != nil || !changed {
		t.Fatalf("mark unavailable changed=%v err=%v", changed, err)
	}
	if changed, err := s.MarkSessionCostUsageUnavailable("cost", "cursor-5"); err != nil || changed {
		t.Fatalf("repeat unavailable changed=%v err=%v", changed, err)
	}
	state, _ = s.SessionCost("cost")
	if !state.UsageUnavailable || state.Cursor != "cursor-5" {
		t.Fatalf("unavailable state = %+v", state)
	}
}

func TestSessionCostSourceCursorsPersistIndependently(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s.Add(&protocol.Session{ID: "usage", Label: "usage"})
	if err := s.InitializeSessionCostSources("usage", map[string]string{
		"root": "root-head", "child": "child-head",
	}); err != nil {
		t.Fatal(err)
	}
	observation := SessionCostObservation{
		ObservationID: "native:child:message", Model: "claude-sonnet-4-5",
		Usage: sessioncost.Usage{InputTokens: 3, OutputTokens: 5},
	}
	if changed, err := s.ApplySessionCostSourceObservations("usage", "child", "child-next", []SessionCostObservation{observation}); err != nil || !changed {
		t.Fatalf("apply changed=%v err=%v", changed, err)
	}
	if _, err := s.MarkSessionCostMeasurementIncomplete("usage"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state, err := s.SessionCost("usage")
	if err != nil {
		t.Fatal(err)
	}
	if state.Sources["root"].Cursor != "root-head" || state.Sources["child"].Cursor != "child-next" {
		t.Fatalf("source cursors = %+v", state.Sources)
	}
	if !state.MeasurementIncomplete || state.Ledger[sessioncost.AgentKey("claude-sonnet-4-5")] != observation.Usage {
		t.Fatalf("reopened state = %+v", state)
	}
}
func TestSessionCostSeparatesGuardianTrafficFromTheAgentsOwn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
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
	s, err := NewWithDB(dbPath)
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
