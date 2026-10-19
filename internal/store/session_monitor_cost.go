package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
	"log"
	"maps"
	"slices"
	"strings"
	"time"
)

const LegacySessionCostSource = "_legacy"

type SessionMonitorCost struct {
	Initialized bool                          `json:"initialized,omitempty"`
	Sources     map[string]*SessionCostSource `json:"sources,omitempty"`
}

type SessionCostSource struct {
	Initialized           bool                              `json:"initialized,omitempty"`
	Cursor                string                            `json:"cursor,omitempty"`
	UsageUnavailable      bool                              `json:"usage_unavailable,omitempty"`
	MeasurementIncomplete bool                              `json:"measurement_incomplete,omitempty"`
	Ledger                sessioncost.Ledger                `json:"ledger,omitempty"`
	Observations          map[string]SessionCostObservation `json:"observations,omitempty"`
	Finalized             []string                          `json:"finalized,omitempty"`
}

func (source *SessionCostSource) Clone() *SessionCostSource {
	if source == nil {
		return &SessionCostSource{}
	}
	clone := *source
	clone.Ledger = maps.Clone(source.Ledger)
	clone.Observations = maps.Clone(source.Observations)
	clone.Finalized = slices.Clone(source.Finalized)
	return &clone
}

func (state SessionMonitorCost) Clone() SessionMonitorCost {
	clone := SessionMonitorCost{Initialized: state.Initialized, Sources: make(map[string]*SessionCostSource, len(state.Sources))}
	for id, source := range state.Sources {
		clone.Sources[id] = source.Clone()
	}
	return clone
}

type SessionCostUsage struct {
	UsageUnavailable      bool
	MeasurementIncomplete bool
	Ledger                sessioncost.Ledger
}

func (state SessionMonitorCost) Usage() SessionCostUsage {
	usage := SessionCostUsage{Ledger: make(sessioncost.Ledger)}
	for _, source := range state.Sources {
		usage.UsageUnavailable = usage.UsageUnavailable || source.UsageUnavailable
		usage.MeasurementIncomplete = usage.MeasurementIncomplete || source.MeasurementIncomplete
		for key, value := range source.Ledger {
			usage.Ledger[key] = usage.Ledger[key].Add(value)
		}
	}
	return usage
}

// sessionCostSaveInterval spaces saves of transcript-derived cost: each save is a consistent
// snapshot with its cursor, so after a crash re-reading from that cursor rebuilds the rest.
const sessionCostSaveInterval = 30 * time.Second

type sessionCostEntry struct {
	state   SessionMonitorCost
	unsaved bool
	savedAt time.Time
}

func (s *Store) liveSessionCost(sessionID protocol.SessionID) (*sessionCostEntry, bool, error) {
	s.costMu.Lock()
	defer s.costMu.Unlock()
	if entry := s.liveCosts[sessionID]; entry != nil {
		return entry, true, nil
	}
	var raw, closedAt string
	if err := s.db.QueryRow("SELECT session_cost_json, closed_at FROM sessions WHERE id = ?", sessionID).Scan(&raw, &closedAt); err != nil {
		return nil, false, err
	}
	state, err := decodeSessionMonitorCost(raw)
	if err != nil {
		return nil, false, fmt.Errorf("decode session cost for %s: %w", sessionID, err)
	}
	entry := &sessionCostEntry{state: state}
	if closedAt != "" {
		return entry, false, nil
	}
	if s.liveCosts == nil {
		s.liveCosts = make(map[protocol.SessionID]*sessionCostEntry)
	}
	s.liveCosts[sessionID] = entry
	return entry, true, nil
}

func (s *Store) forgetSessionCost(sessionID protocol.SessionID) {
	s.costMu.Lock()
	delete(s.liveCosts, sessionID)
	s.costMu.Unlock()
}

func (s *Store) writeSessionCost(sessionID protocol.SessionID, state SessionMonitorCost) error {
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
func (s *Store) saveUnsavedSessionCostTx(tx *sql.Tx, sessionID protocol.SessionID) error {
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

func decodeSessionMonitorCost(raw string) (SessionMonitorCost, error) {
	var state SessionMonitorCost
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return state, err
		}
	}
	return state, nil
}

func (s *Store) SessionCostCheckpoint(sessionID protocol.SessionID) (SessionMonitorCost, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return s.sessionCosts[sessionID].Clone(), nil
	}
	entry, _, err := s.liveSessionCost(sessionID)
	if err != nil {
		return SessionMonitorCost{}, err
	}
	return entry.state.Clone(), nil
}

func (s *Store) SessionCost(sessionID protocol.SessionID) (SessionCostUsage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return s.sessionCosts[sessionID].Usage(), nil
	}
	entry, _, err := s.liveSessionCost(sessionID)
	if err != nil {
		return SessionCostUsage{}, err
	}
	return entry.state.Usage(), nil
}

func (s *Store) SessionCostUsages(sessionIDs []protocol.SessionID) (map[protocol.SessionID]SessionCostUsage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	usages := make(map[protocol.SessionID]SessionCostUsage, len(sessionIDs))
	var stored []protocol.SessionID
	s.costMu.Lock()
	for _, id := range sessionIDs {
		state, cached := s.sessionCosts[id], s.db == nil
		if entry := s.liveCosts[id]; entry != nil {
			state, cached = entry.state, true
		}
		if cached {
			usages[id] = state.Usage()
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
		var id protocol.SessionID
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, fmt.Errorf("read session costs: %w", err)
		}
		state, err := decodeSessionMonitorCost(raw)
		if err != nil {
			log.Printf("[store] session cost: decoding %s: %v", id, err)
			continue
		}
		usages[id] = state.Usage()
	}
	return usages, rows.Err()
}

func (s *Store) ApplySessionCostSnapshot(sessionID protocol.SessionID, state SessionMonitorCost, durable bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		if s.sessionIsLiveLocked(sessionID) {
			s.sessionCosts[sessionID] = state
		}
		return nil
	}
	entry, live, err := s.liveSessionCost(sessionID)
	if err != nil || !live {
		return err
	}
	entry.state = state
	now := time.Now()
	if !durable && now.Sub(entry.savedAt) < sessionCostSaveInterval {
		entry.unsaved = true
		return nil
	}
	if err := s.writeSessionCost(sessionID, entry.state); err != nil {
		s.forgetSessionCost(sessionID)
		return err
	}
	entry.unsaved, entry.savedAt = false, now
	return nil
}

func finalizeSessionCost(state *SessionMonitorCost) bool {
	changed := false
	state.Sources = maps.Clone(state.Sources)
	for id, source := range state.Sources {
		if len(source.Observations) == 0 {
			continue
		}
		changed = true
		source = source.Clone()
		for key := range source.Observations {
			source.Finalized = append(source.Finalized, key)
		}
		slices.Sort(source.Finalized)
		source.Finalized = slices.Compact(source.Finalized)
		source.Observations = nil
		state.Sources[id] = source
	}
	return changed
}
