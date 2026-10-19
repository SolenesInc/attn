package store

import (
	"log"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type SessionRuntimeState struct {
	State              protocol.SessionState
	Since              time.Time
	Updated            time.Time
	Turn               TurnStamps
	LastModelRequestAt time.Time
	Driver             AgentDriverReportCursor
}

func (s *Store) ApplySessionRuntimeState(id protocol.SessionID, state SessionRuntimeState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		session := s.sessions[id]
		if session == nil {
			return false
		}
		session.State = state.State
		session.StateSince = string(protocol.NewTimestamp(state.Since))
		session.StateUpdatedAt = string(protocol.NewTimestamp(state.Updated))
		if !state.LastModelRequestAt.IsZero() {
			session.LastModelRequestAt = protocol.Ptr(string(protocol.NewTimestamp(state.LastModelRequestAt)))
		}
		s.setTurnStampsLocked(id, state.Turn)
		if state.Driver.RunID != "" {
			s.agentDriverRuns[id] = state.Driver
		}
		return true
	}
	request := ""
	if !state.LastModelRequestAt.IsZero() {
		request = string(protocol.NewTimestamp(state.LastModelRequestAt))
	}
	result, err := s.db.Exec(`UPDATE sessions SET state = ?, state_since = ?, state_updated_at = ?,
		turn_opened_at = ?, turn_settled_at = ?, turn_snoozed_until = ?, last_model_request_at = NULLIF(?, ''),
		agent_driver_report_seq = CASE WHEN agent_driver_run_id = ? THEN max(agent_driver_report_seq, ?) ELSE agent_driver_report_seq END
		WHERE id = ? AND closed_at = ''`, string(state.State), string(protocol.NewTimestamp(state.Since)), string(protocol.NewTimestamp(state.Updated)),
		turnStamp(state.Turn.OpenedAt), turnStamp(state.Turn.SettledAt), turnStamp(state.Turn.SnoozedUntil), request, state.Driver.RunID, state.Driver.Seq, id)
	if err != nil {
		log.Printf("[store] apply monitor state: session=%s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

func turnStamp(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(sortableTimeFormat)
}
