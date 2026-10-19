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
