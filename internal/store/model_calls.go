package store

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const modelCallTimeLayout = "2006-01-02T15:04:05.000Z"

func (s *Store) AddModelCall(profileID string, call protocol.ModelCall) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO model_calls(profile_id, at, sender, purpose, harness, model, took_ms, model_ms, tokens_in, tokens_out, cost_usd, outcome)
		SELECT ?,?,?,?,?,?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM profiles WHERE id = ? AND deleted_at = '')`,
		profileID, call.At, call.Sender, call.Purpose, call.Harness, call.Model, call.TookMs, protocol.Deref(call.ModelMs), call.TokensIn, call.TokensOut, call.CostUsd, string(call.Outcome), profileID)
	return err
}

func ModelCallTime(at time.Time) string {
	return at.UTC().Format(modelCallTimeLayout)
}

func (s *Store) ModelCalls(profileID, sender string) ([]protocol.ModelCall, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT at, sender, purpose, harness, model, took_ms, model_ms, tokens_in, tokens_out, cost_usd, outcome FROM model_calls
		WHERE profile_id = ? AND (? = '' OR sender = ?) ORDER BY at DESC, id DESC`, profileID, sender, sender)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	calls := []protocol.ModelCall{}
	for rows.Next() {
		var call protocol.ModelCall
		var modelMs int
		var outcome string
		if err := rows.Scan(&call.At, &call.Sender, &call.Purpose, &call.Harness, &call.Model, &call.TookMs, &modelMs, &call.TokensIn, &call.TokensOut, &call.CostUsd, &outcome); err != nil {
			return nil, err
		}
		if modelMs > 0 {
			call.ModelMs = protocol.Ptr(modelMs)
		}
		call.Outcome = protocol.ModelCallOutcome(outcome)
		calls = append(calls, call)
	}
	return calls, rows.Err()
}

func (s *Store) PruneModelCalls(before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`DELETE FROM model_calls WHERE at < ?`, ModelCallTime(before))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
