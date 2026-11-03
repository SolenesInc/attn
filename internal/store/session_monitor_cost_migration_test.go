package store

import (
	"encoding/json"
	"github.com/victorarias/attn/internal/sessioncost"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestPerSourceCostsPreserveAccountingAndCursorsAcrossUpgradeAndRestart(t *testing.T) {
	cases := []struct{ name, old, want string }{
		{"unknown-resume", "", `{}`},
		{"fresh-empty", `{"initialized":true}`, `{"initialized":true}`},
		{"single-cursor", `{"initialized":true,"cursor":"root-head"}`, `{"initialized":true,"sources":{"_legacy":{"initialized":true,"cursor":"root-head"}}}`},
		{"legacy-model-key", `{"initialized":true,"ledger":{"claude-opus-4-8":{"input_tokens":10,"output_tokens":2}}}`, `{"initialized":true,"sources":{"_legacy":{"initialized":true,"ledger":{"agent|claude-opus-4-8":{"input_tokens":10,"output_tokens":2}}}}}`},
		{"source-cursors", `{"initialized":true,"cursor":"obsolete","sources":{"/root":{"cursor":"root-cursor"},"/child":{"cursor":"child-cursor"}},"ledger":{"agent|claude-opus-4-8":{"input_tokens":10,"output_tokens":2}}}`, `{"initialized":true,"sources":{"/root":{"initialized":true,"cursor":"root-cursor"},"/child":{"initialized":true,"cursor":"child-cursor"},"_legacy":{"initialized":true,"ledger":{"agent|claude-opus-4-8":{"input_tokens":10,"output_tokens":2}}}}}`},
		{"pending-and-finalized", `{"initialized":true,"ledger":{"agent|claude-opus-4-8":{"input_tokens":10,"output_tokens":2}},"observations":{"claude:pending":{"observation_id":"claude:pending","model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":2}}},"finalized":["claude:closed"]}`, `{"initialized":true,"sources":{"_legacy":{"initialized":true,"ledger":{"agent|claude-opus-4-8":{"input_tokens":10,"output_tokens":2}},"observations":{"claude:pending":{"observation_id":"claude:pending","model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":2}}},"finalized":["claude:closed"]}}}`},
		{"incomplete-before-baseline", `{"measurement_incomplete":true}`, `{"sources":{"_legacy":{"measurement_incomplete":true}}}`},
		{"unavailable", `{"initialized":true,"usage_unavailable":true}`, `{"initialized":true,"sources":{"_legacy":{"initialized":true,"usage_unavailable":true}}}`},
	}
	path := filepath.Join(t.TempDir(), "costs.db")
	db, err := OpenDBAtSchemaVersion(path, 1792371295625260)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		for _, closed := range []bool{false, true} {
			id, closedAt := c.name, ""
			if closed {
				id += "-closed"
				closedAt = "2026-10-19T00:00:00Z"
			}
			if _, err := db.Exec(`INSERT INTO sessions(id,user_label,directory,agent,state,state_since,state_updated_at,last_seen,closed_at,session_cost_json) VALUES(?,?,'/tmp/shop','claude','idle','','','',?,?)`, id, id, closedAt, c.old); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for restart := range 2 {
		s, err := NewWithDB(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cases {
			for _, closed := range []bool{false, true} {
				id := c.name
				if closed {
					id += "-closed"
				}
				got, err := s.SessionCostCheckpoint(protocol.SessionID(id))
				if err != nil {
					t.Fatal(err)
				}
				var want sessioncost.Checkpoint
				if err := json.Unmarshal([]byte(c.want), &want); err != nil {
					t.Fatal(err)
				}
				if len(got.Sources) == 0 {
					got.Sources = nil
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("restart%d %s checkpoint=%+v, want %+v", restart, id, got, want)
				}
				usage, err := s.SessionCost(protocol.SessionID(id))
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(usage, want.Usage()) {
					t.Errorf("restart%d %s usage=%+v, want %+v", restart, id, usage, want.Usage())
				}
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUpgradeDropsCodexCostObservationsAndKeepsTotalsAndClaudeDeduplication(t *testing.T) {
	const old = `{"initialized":true,"sources":{` +
		`"/rollout.jsonl":{"initialized":true,"cursor":"root-cursor","ledger":{"agent|gpt-5.5":{"input_tokens":10,"output_tokens":2}},"observations":{"codex:v1:aa:10:0":{"observation_id":"codex:v1:aa:10:0","model":"gpt-5.5","usage":{"input_tokens":10,"output_tokens":2}}}},` +
		`"thread-1":{"initialized":true,"cursor":"child-cursor","ledger":{"guardian|codex-auto-review":{"input_tokens":5,"output_tokens":1}},"observations":{"native:thread-1:codex:v1:bb:20:0":{"observation_id":"native:thread-1:codex:v1:bb:20:0","model":"codex-auto-review","purpose":"guardian","usage":{"input_tokens":5,"output_tokens":1}}},"finalized":["native:thread-1:codex:v1:bb:10:0"]},` +
		`"/claude.jsonl":{"initialized":true,"cursor":"claude-cursor","ledger":{"agent|claude-opus-4-8":{"input_tokens":7,"output_tokens":3}},"observations":{"claude:msg":{"observation_id":"claude:msg","model":"claude-opus-4-8","usage":{"input_tokens":7,"output_tokens":3}},"native:/claude/subagents/a.jsonl:claude:sub":{"observation_id":"native:/claude/subagents/a.jsonl:claude:sub","model":"claude-opus-4-8","usage":{"input_tokens":0,"output_tokens":1}}},"finalized":["claude:closed","codex:v1:aa:5:0"]}}}`
	const want = `{"initialized":true,"sources":{` +
		`"/rollout.jsonl":{"initialized":true,"cursor":"root-cursor","ledger":{"agent|gpt-5.5":{"input_tokens":10,"output_tokens":2}}},` +
		`"thread-1":{"initialized":true,"cursor":"child-cursor","ledger":{"guardian|codex-auto-review":{"input_tokens":5,"output_tokens":1}}},` +
		`"/claude.jsonl":{"initialized":true,"cursor":"claude-cursor","ledger":{"agent|claude-opus-4-8":{"input_tokens":7,"output_tokens":3}},"observations":{"claude:msg":{"observation_id":"claude:msg","model":"claude-opus-4-8","usage":{"input_tokens":7,"output_tokens":3}},"native:/claude/subagents/a.jsonl:claude:sub":{"observation_id":"native:/claude/subagents/a.jsonl:claude:sub","model":"claude-opus-4-8","usage":{"input_tokens":0,"output_tokens":1}}},"finalized":["claude:closed"]}}}`
	path := filepath.Join(t.TempDir(), "costs.db")
	db, err := OpenDBAtSchemaVersion(path, 1793543191592952)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"live", "closed"} {
		closedAt := ""
		if id == "closed" {
			closedAt = "2026-11-01T00:00:00Z"
		}
		if _, err := db.Exec(`INSERT INTO sessions(id,user_label,directory,closed_at) VALUES(?,?,'/tmp/shop',?)`, id, id, closedAt); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO session_runtime(session_id,session_cost_json) VALUES(?,?)`, id, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var before, expected sessioncost.Checkpoint
	if err := json.Unmarshal([]byte(old), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []protocol.SessionID{"live", "closed"} {
		got, err := s.SessionCostCheckpoint(id)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("%s checkpoint=%+v, want %+v", id, got, expected)
		}
		usage, err := s.SessionCost(id)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(usage, before.Usage()) {
			t.Errorf("%s usage=%+v, want the pre-upgrade %+v", id, usage, before.Usage())
		}
	}
}
