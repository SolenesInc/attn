package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/sessioncost"
)

type SessionCostObservation struct {
	ObservationID string            `json:"observation_id"`
	Model         string            `json:"model"`
	Purpose       string            `json:"purpose,omitempty"`
	FastMode      bool              `json:"fast_mode,omitempty"`
	Usage         sessioncost.Usage `json:"usage"`
}

func (o SessionCostObservation) ledgerKey() sessioncost.LedgerKey {
	key := sessioncost.RequestLedgerKey(o.Model, o.Purpose, o.Usage)
	key.FastMode = o.FastMode
	return key
}

type SessionCostState struct {
	Initialized           bool                              `json:"initialized,omitempty"`
	Cursor                string                            `json:"cursor,omitempty"`
	UsageUnavailable      bool                              `json:"usage_unavailable,omitempty"`
	MeasurementIncomplete bool                              `json:"measurement_incomplete,omitempty"`
	Sources               map[string]SessionCostSourceState `json:"sources,omitempty"`
	Ledger                sessioncost.Ledger                `json:"ledger,omitempty"`
	Observations          map[string]SessionCostObservation `json:"observations,omitempty"`
	Finalized             []string                          `json:"finalized,omitempty"`
}

type SessionCostSourceState struct {
	Cursor string `json:"cursor,omitempty"`
}

func cloneSessionCostState(state SessionCostState) SessionCostState {
	clone := SessionCostState{
		Initialized: state.Initialized, Cursor: state.Cursor, UsageUnavailable: state.UsageUnavailable,
		MeasurementIncomplete: state.MeasurementIncomplete,
	}
	if state.Sources != nil {
		clone.Sources = make(map[string]SessionCostSourceState, len(state.Sources))
		for id, source := range state.Sources {
			clone.Sources[id] = source
		}
	}
	if state.Ledger != nil {
		clone.Ledger = make(sessioncost.Ledger, len(state.Ledger))
		for key, usage := range state.Ledger {
			clone.Ledger[key] = usage
		}
	}
	if state.Observations != nil {
		clone.Observations = make(map[string]SessionCostObservation, len(state.Observations))
		for id, observation := range state.Observations {
			clone.Observations[id] = observation
		}
	}
	if state.Finalized != nil {
		clone.Finalized = append([]string(nil), state.Finalized...)
	}
	return clone
}

// costView is what readers get: the ledger, flags and cursors, without the per-request observations
// that only the store needs and that grow for the life of a session.
func costView(state SessionCostState) SessionCostState {
	state.Observations = nil
	state.Finalized = nil
	return cloneSessionCostState(state)
}

// sessionCostSaveInterval spaces saves of transcript-derived cost: each save is a consistent
// snapshot with its cursor, so after a crash re-reading from that cursor rebuilds the rest.
const sessionCostSaveInterval = 30 * time.Second

type sessionCostEntry struct {
	state   SessionCostState
	unsaved bool
	savedAt time.Time
}

func (s *Store) liveSessionCost(sessionID string) (*sessionCostEntry, bool, error) {
	s.costMu.Lock()
	defer s.costMu.Unlock()
	if entry := s.liveCosts[sessionID]; entry != nil {
		return entry, true, nil
	}
	var raw, closedAt string
	if err := s.db.QueryRow("SELECT session_cost_json, closed_at FROM sessions WHERE id = ?", sessionID).Scan(&raw, &closedAt); err != nil {
		return nil, false, err
	}
	state, err := decodeSessionCostState(raw)
	if err != nil {
		return nil, false, fmt.Errorf("decode session cost for %s: %w", sessionID, err)
	}
	entry := &sessionCostEntry{state: state}
	if closedAt != "" {
		return entry, false, nil
	}
	if s.liveCosts == nil {
		s.liveCosts = make(map[string]*sessionCostEntry)
	}
	s.liveCosts[sessionID] = entry
	return entry, true, nil
}

func (s *Store) forgetSessionCost(sessionID string) {
	s.costMu.Lock()
	delete(s.liveCosts, sessionID)
	s.costMu.Unlock()
}

func (s *Store) writeSessionCost(sessionID string, state SessionCostState) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode session cost for %s: %w", sessionID, err)
	}
	_, err = s.db.Exec("UPDATE sessions SET session_cost_json = ? WHERE id = ? AND closed_at = ''", string(encoded), sessionID)
	return err
}

func (s *Store) flushSessionCosts() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.costMu.Lock()
	defer s.costMu.Unlock()
	for sessionID, entry := range s.liveCosts {
		if !entry.unsaved {
			continue
		}
		if err := s.writeSessionCost(sessionID, entry.state); err != nil {
			log.Printf("[store] session cost: saving %s: %v", sessionID, err)
			continue
		}
		entry.unsaved = false
	}
}

// saveUnsavedSessionCostTx writes a live session's pending cost into tx, so a close finalizes it.
func (s *Store) saveUnsavedSessionCostTx(tx *sql.Tx, sessionID string) error {
	s.costMu.Lock()
	entry := s.liveCosts[sessionID]
	s.costMu.Unlock()
	if entry == nil || !entry.unsaved {
		return nil
	}
	encoded, err := json.Marshal(entry.state)
	if err != nil {
		return fmt.Errorf("encode session cost for %s: %w", sessionID, err)
	}
	_, err = tx.Exec("UPDATE sessions SET session_cost_json = ? WHERE id = ?", string(encoded), sessionID)
	return err
}

func decodeSessionCostState(raw string) (SessionCostState, error) {
	state := SessionCostState{}
	if strings.TrimSpace(raw) == "" {
		return state, nil
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return SessionCostState{}, err
	}
	return state, nil
}

func (s *Store) SessionCost(sessionID string) (SessionCostState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return costView(s.sessionCosts[sessionID]), nil
	}
	entry, _, err := s.liveSessionCost(sessionID)
	if err != nil {
		return SessionCostState{}, err
	}
	return costView(entry.state), nil
}

type SessionCostUsage struct {
	UsageUnavailable      bool               `json:"usage_unavailable,omitempty"`
	MeasurementIncomplete bool               `json:"measurement_incomplete,omitempty"`
	Ledger                sessioncost.Ledger `json:"ledger,omitempty"`
}

func (s *Store) SessionCostUsages(sessionIDs []string) (map[string]SessionCostUsage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	usages := make(map[string]SessionCostUsage, len(sessionIDs))
	var stored []string
	s.costMu.Lock()
	for _, id := range sessionIDs {
		state, cached := s.sessionCosts[id], s.db == nil
		if entry := s.liveCosts[id]; entry != nil {
			state, cached = entry.state, true
		}
		if cached {
			usages[id] = SessionCostUsage{state.UsageUnavailable, state.MeasurementIncomplete, maps.Clone(state.Ledger)}
		} else {
			stored = append(stored, id)
		}
	}
	s.costMu.Unlock()
	if len(stored) == 0 {
		return usages, nil
	}
	ids, err := json.Marshal(stored)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query("SELECT id, session_cost_json FROM sessions WHERE id IN (SELECT value FROM json_each(?))", string(ids))
	if err != nil {
		return nil, fmt.Errorf("read session costs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, fmt.Errorf("read session costs: %w", err)
		}
		var usage SessionCostUsage
		if strings.TrimSpace(raw) != "" {
			if err := json.Unmarshal([]byte(raw), &usage); err != nil {
				log.Printf("[store] session cost: decoding %s: %v", id, err)
				continue
			}
		}
		usages[id] = usage
	}
	return usages, rows.Err()
}

func (s *Store) SetSessionCostCursor(sessionID, cursor string) error {
	return s.updateSessionCost(sessionID, func(state *SessionCostState) bool {
		durable := !state.Initialized
		state.Initialized = true
		state.Cursor = strings.TrimSpace(cursor)
		return durable
	})
}

func (s *Store) InitializeSessionCostTracking(sessionID string) error {
	return s.updateSessionCost(sessionID, func(state *SessionCostState) bool {
		durable := !state.Initialized
		state.Initialized = true
		return durable
	})
}

func (s *Store) InitializeSessionCostSources(sessionID string, cursors map[string]string) error {
	return s.updateSessionCost(sessionID, func(state *SessionCostState) bool {
		durable := !state.Initialized
		if state.Sources == nil {
			state.Sources = make(map[string]SessionCostSourceState)
		}
		for id, cursor := range cursors {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, exists := state.Sources[id]; !exists {
				state.Sources[id] = SessionCostSourceState{Cursor: strings.TrimSpace(cursor)}
				durable = true
			}
		}
		state.Initialized = true
		return durable
	})
}

func (s *Store) SetSessionCostSourceCursor(sessionID, sourceID, cursor string) error {
	return s.updateSessionCost(sessionID, func(state *SessionCostState) bool {
		return setSessionCostSourceCursor(state, sourceID, cursor)
	})
}

func setSessionCostSourceCursor(state *SessionCostState, sourceID, cursor string) (durable bool) {
	sourceID = strings.TrimSpace(sourceID)
	if state.Sources == nil {
		state.Sources = make(map[string]SessionCostSourceState)
	}
	_, known := state.Sources[sourceID]
	durable = !state.Initialized || !known
	state.Initialized = true
	state.Sources[sourceID] = SessionCostSourceState{Cursor: strings.TrimSpace(cursor)}
	return durable
}

func (s *Store) MarkSessionCostMeasurementIncomplete(sessionID string) (bool, error) {
	changed := false
	err := s.updateSessionCost(sessionID, func(state *SessionCostState) bool {
		changed = !state.MeasurementIncomplete
		state.MeasurementIncomplete = true
		return changed
	})
	return changed, err
}

func (s *Store) MarkSessionCostUsageUnavailable(sessionID, cursor string) (bool, error) {
	changed := false
	err := s.updateSessionCost(sessionID, func(state *SessionCostState) bool {
		durable := !state.Initialized
		state.Initialized = true
		changed = !state.UsageUnavailable
		state.UsageUnavailable = true
		state.Cursor = strings.TrimSpace(cursor)
		return durable || changed
	})
	return changed, err
}

func (s *Store) ApplySessionCostObservations(sessionID, cursor string, observations []SessionCostObservation) (bool, error) {
	changed := false
	err := s.updateSessionCost(sessionID, func(state *SessionCostState) bool {
		durable := !state.Initialized
		changed = applySessionCostObservations(sessionID, state, observations)
		state.Cursor = strings.TrimSpace(cursor)
		return durable
	})
	return changed, err
}

func (s *Store) ApplySessionCostSourceObservations(sessionID, sourceID, cursor string, observations []SessionCostObservation) (bool, error) {
	changed := false
	err := s.updateSessionCost(sessionID, func(state *SessionCostState) bool {
		durable := setSessionCostSourceCursor(state, sourceID, cursor)
		changed = applySessionCostObservations(sessionID, state, observations)
		return durable
	})
	return changed, err
}

func applySessionCostObservations(sessionID string, state *SessionCostState, observations []SessionCostObservation) bool {
	state.Initialized = true
	if state.Ledger == nil {
		state.Ledger = make(sessioncost.Ledger)
	}
	if state.Observations == nil {
		state.Observations = make(map[string]SessionCostObservation)
	}
	changed := false
	finalized := finalizedSet(state.Finalized)
	for _, observation := range observations {
		observation.ObservationID = strings.TrimSpace(observation.ObservationID)
		observation.Model = strings.TrimSpace(observation.Model)
		observation.Purpose = sessioncost.NewLedgerKey(observation.Model, observation.Purpose).Purpose
		if observation.ObservationID == "" || observation.Model == "" || !observation.Usage.HasUsage() {
			continue
		}
		if _, final := finalized[observation.ObservationID]; final {
			log.Printf("[store] session cost: refused %s for session %s: a close finalized it",
				observation.ObservationID, sessionID)
			continue
		}
		prior, exists := state.Observations[observation.ObservationID]
		if exists && reflect.DeepEqual(prior, observation) {
			continue
		}
		if exists {
			priorKey := prior.ledgerKey()
			state.Ledger[priorKey] = state.Ledger[priorKey].Subtract(prior.Usage)
		}
		key := observation.ledgerKey()
		state.Ledger[key] = state.Ledger[key].Add(observation.Usage)
		state.Observations[observation.ObservationID] = observation
		changed = true
	}
	return changed
}

func rekeyLongContextObservations(state *SessionCostState, onlyModel string) bool {
	if state.Ledger == nil {
		return false
	}
	changed := false
	for _, observation := range state.Observations {
		if onlyModel != "" && observation.Model != onlyModel || onlyModel == "" && observation.Model == "gpt-6.1-sol" {
			continue
		}
		standard := sessioncost.NewLedgerKey(observation.Model, observation.Purpose)
		key := observation.ledgerKey()
		if key == standard {
			continue
		}
		state.Ledger[standard] = state.Ledger[standard].Subtract(observation.Usage)
		if state.Ledger[standard] == (sessioncost.Usage{}) {
			delete(state.Ledger, standard)
		}
		state.Ledger[key] = state.Ledger[key].Add(observation.Usage)
		changed = true
	}
	return changed
}

func finalizedSet(ids []string) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

func finalizeSessionCost(state *SessionCostState) {
	if len(state.Observations) == 0 {
		return
	}
	finalized := state.Finalized
	for id := range state.Observations {
		finalized = append(finalized, id)
	}
	slices.Sort(finalized)
	state.Finalized = slices.Compact(finalized)
	state.Observations = nil
}

// updateSessionCost applies mutate, which reports a change a transcript re-read cannot rebuild;
// that is written at once, anything else at most every sessionCostSaveInterval.
func (s *Store) updateSessionCost(sessionID string, mutate func(*SessionCostState) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		if !s.sessionIsLiveLocked(sessionID) {
			return nil
		}
		if s.sessionCosts == nil {
			s.sessionCosts = make(map[string]SessionCostState)
		}
		state := cloneSessionCostState(s.sessionCosts[sessionID])
		mutate(&state)
		s.sessionCosts[sessionID] = state
		return nil
	}

	entry, live, err := s.liveSessionCost(sessionID)
	if err != nil || !live {
		return err
	}
	now := time.Now()
	if !mutate(&entry.state) && now.Sub(entry.savedAt) < sessionCostSaveInterval {
		entry.unsaved = true
		return nil
	}
	if err := s.writeSessionCost(sessionID, entry.state); err != nil {
		s.forgetSessionCost(sessionID)
		return err
	}
	entry.unsaved = false
	entry.savedAt = now
	return nil
}
