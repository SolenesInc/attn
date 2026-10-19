package store

import (
	"log"
	"time"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

type SessionRuntimeState struct {
	State              protocol.SessionState
	Since              time.Time
	Updated            time.Time
	Turn               TurnStamps
	LastModelRequestAt time.Time
	Driver             AgentDriverReportCursor
	DriverMetadata     string
	DriverTranscript   string
	Branch             git.BranchInfo
	HarnessTitle       string
}

func (s *Store) ApplySessionRuntimeState(id protocol.SessionID, state SessionRuntimeState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		session := s.sessions[id]
		if session == nil {
			return false
		}
		cursor := s.agentDriverRuns[id]
		if state.Driver.RunID != "" && cursor.RunID != state.Driver.RunID {
			return false
		}
		session.State = state.State
		session.StateSince = string(protocol.NewTimestamp(state.Since))
		session.StateUpdatedAt = string(protocol.NewTimestamp(state.Updated))
		labels := s.sessionLabels[id]
		labels.HarnessTitle = state.HarnessTitle
		s.sessionLabels[id] = labels
		session.Label = labels.shown(id, session.Directory, session.Agent)
		session.Branch = nil
		if state.Branch.Branch != "" {
			session.Branch = protocol.Ptr(state.Branch.Branch)
		}
		session.IsWorktree = protocol.Ptr(state.Branch.IsWorktree)
		session.MainRepo = nil
		if state.Branch.MainRepo != "" {
			session.MainRepo = protocol.Ptr(state.Branch.MainRepo)
		}
		session.Repository = nil
		if state.Branch.Repository != "" {
			session.Repository = protocol.Ptr(state.Branch.Repository)
		}
		if !state.LastModelRequestAt.IsZero() {
			session.LastModelRequestAt = protocol.Ptr(string(protocol.NewTimestamp(state.LastModelRequestAt)))
		}
		s.setTurnStampsLocked(id, state.Turn)
		if state.Driver.RunID != "" {
			cursor.Seq = max(cursor.Seq, state.Driver.Seq)
			s.agentDriverRuns[id] = cursor
			s.agentMetadata[id] = state.DriverMetadata
			if s.agentDriverTranscripts == nil {
				s.agentDriverTranscripts = make(map[protocol.SessionID]string)
			}
			s.agentDriverTranscripts[id] = state.DriverTranscript
		}
		return true
	}
	request := ""
	if !state.LastModelRequestAt.IsZero() {
		request = string(protocol.NewTimestamp(state.LastModelRequestAt))
	}
	result, err := s.db.Exec(`UPDATE sessions SET state = ?, state_since = ?, state_updated_at = ?,
		turn_opened_at = ?, turn_settled_at = ?, turn_snoozed_until = ?, turn_agent_at = ?, last_model_request_at = NULLIF(?, ''),
		agent_driver_report_seq = CASE WHEN agent_driver_run_id = ? THEN max(agent_driver_report_seq, ?) ELSE agent_driver_report_seq END,
		agent_metadata = CASE WHEN agent_driver_run_id = NULLIF(?, '') THEN ? ELSE agent_metadata END,
		agent_driver_transcript_path = CASE WHEN agent_driver_run_id = NULLIF(?, '') THEN ? ELSE agent_driver_transcript_path END,
		branch = ?, is_worktree = ?, main_repo = ?, repository = ?, harness_title = ?
		WHERE id = ? AND closed_at = '' AND (? = '' OR agent_driver_run_id = ?)`, string(state.State), string(protocol.NewTimestamp(state.Since)), string(protocol.NewTimestamp(state.Updated)),
		turnStamp(state.Turn.OpenedAt), turnStamp(state.Turn.SettledAt), turnStamp(state.Turn.SnoozedUntil), turnStamp(state.Turn.AgentAt), request, state.Driver.RunID, state.Driver.Seq,
		state.Driver.RunID, state.DriverMetadata, state.Driver.RunID, state.DriverTranscript,
		state.Branch.Branch, boolToInt(state.Branch.IsWorktree), state.Branch.MainRepo, state.Branch.Repository, state.HarnessTitle, id, state.Driver.RunID, state.Driver.RunID)
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
