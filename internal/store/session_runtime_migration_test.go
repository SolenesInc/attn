package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
)

func TestMovingRuntimeOffSessionsKeepsEverySessionsStateTurnDriverBranchTitleAndCost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.db")
	db, err := OpenDBAtSchemaVersion(path, 1792451296733331)
	if err != nil {
		t.Fatal(err)
	}
	const cost = `{"initialized":true,"sources":{"/t/live.jsonl":{"initialized":true,"cursor":"c1","ledger":{"agent|claude-opus-4-8":{"input_tokens":10,"output_tokens":2}}},"_legacy":{"initialized":true,"cursor":"legacy-cursor"}}}`
	if _, err := db.Exec(`INSERT INTO sessions (id, user_label, harness_title, directory, agent, state, state_since, state_updated_at, last_seen,
		branch, is_worktree, main_repo, repository, agent_metadata, agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq,
		agent_driver_transcript_path, turn_opened_at, turn_settled_at, turn_snoozed_until, turn_agent_at, turn_requested_at,
		last_model_request_at, session_cost_json, closed_at)
		VALUES
		('live', '', 'Pricing the cart', '/repos/shop', 'claude', 'waiting_input', '2026-10-01T10:00:00Z', '2026-10-01T10:00:01Z', '2026-10-01T10:00:02Z',
		 'feature/tax', 1, '/repos/shop-main', 'github.com/acme/shop', '{"k":"v"}', 'snipe-plugin', 'run-7', 4,
		 '/t/driver.jsonl', '2026-10-01T10:00:00.000000Z', '', '2026-10-01T11:00:00.000000Z', '2026-10-01T09:59:00.000000Z', '2026-10-01T09:58:00.000000Z',
		 '2026-10-01T09:59:30Z', ?, ''),
		('done', 'Shipped', '', '/repos/shop', 'codex', 'idle', '2026-09-01T10:00:00Z', '2026-09-01T10:00:00Z', '2026-09-01T10:00:00Z',
		 NULL, 0, NULL, '', '', '', '', 0, '', '', '', '', '', '', NULL, ?, '2026-09-02T10:00:00Z')`, cost, cost); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	live := s.Get("live")
	if live == nil {
		t.Fatal("the open session disappeared")
	}
	if live.State != protocol.SessionStateWaitingInput || live.StateSince != "2026-10-01T10:00:00Z" || live.LastSeen != "2026-10-01T10:00:02Z" {
		t.Errorf("state %s since %s last seen %s, want waiting_input since 10:00:00 seen 10:00:02", live.State, live.StateSince, live.LastSeen)
	}
	if protocol.Deref(live.Branch) != "feature/tax" || !protocol.Deref(live.IsWorktree) || protocol.Deref(live.MainRepo) != "/repos/shop-main" || protocol.Deref(live.Repository) != "github.com/acme/shop" {
		t.Errorf("branch %+v, want feature/tax in a worktree of /repos/shop-main", live)
	}
	if live.Label != "Pricing the cart" {
		t.Errorf("label %q, want the harness title", live.Label)
	}
	if got := protocol.Deref(live.LastModelRequestAt); got != "2026-10-01T09:59:30Z" {
		t.Errorf("last model request %q, want 09:59:30", got)
	}
	stamps := s.TurnStamps("live")
	if !stamps.OpenedAt.Equal(time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)) || !stamps.SnoozedUntil.Equal(time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC)) {
		t.Errorf("turn stamps %+v, want opened at 10:00 and snoozed until 11:00", stamps)
	}
	if run := s.GetAgentDriverRun("live"); run.PluginName != "snipe-plugin" || run.RunID != "run-7" || run.Seq != 4 {
		t.Errorf("driver run %+v, want snipe-plugin run-7 at 4", run)
	}
	for _, id := range []protocol.SessionID{"live", "done"} {
		totals, err := s.SessionCost(id)
		if err != nil {
			t.Fatal(err)
		}
		if got := totals.Ledger[sessioncost.AgentKey("claude-opus-4-8")]; got.InputTokens != 10 || got.OutputTokens != 2 {
			t.Errorf("%s cost %+v, want 10 in and 2 out", id, totals.Ledger)
		}
	}
	checkpoint, err := s.SessionCostCheckpoint("live")
	if err != nil {
		t.Fatal(err)
	}
	if legacy := checkpoint.Sources["_legacy"]; legacy == nil || legacy.Cursor != "legacy-cursor" {
		t.Errorf("the legacy cost source after the move: %+v, want its cursor kept", legacy)
	}
	done := s.SessionLedgerEntry("done")
	if done == nil || done.Label != "Shipped" || done.State != protocol.SessionStateIdle {
		t.Errorf("closed session in the ledger: %+v, want Shipped and idle", done)
	}
	var left int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name IN ('state', 'last_seen', 'session_cost_json', 'agent_driver_run_id')`).Scan(&left); err != nil || left != 0 {
		t.Errorf("sessions still has %d runtime columns (%v), want none", left, err)
	}
}
