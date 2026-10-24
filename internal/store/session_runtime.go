package store

import (
	"database/sql"
	"errors"
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
	LastSeen           time.Time
	PendingSwitch      string
}

func (s *Store) SaveSessionRuntime(id protocol.SessionID, state SessionRuntimeState) bool {
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
	lastSeen := ""
	if !state.LastSeen.IsZero() {
		lastSeen = string(protocol.NewTimestamp(state.LastSeen))
	}
	result, err := s.db.Exec(`INSERT INTO session_runtime (session_id, state, state_since, state_updated_at,
		turn_opened_at, turn_settled_at, turn_snoozed_until, turn_agent_at, turn_requested_at, last_model_request_at,
		agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq, agent_metadata, agent_driver_transcript_path,
		branch, is_worktree, main_repo, repository, harness_title, last_seen, pending_switch)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		WHERE EXISTS (SELECT 1 FROM sessions WHERE id = ? AND closed_at = '')
		ON CONFLICT (session_id) DO UPDATE SET state = excluded.state, state_since = excluded.state_since, state_updated_at = excluded.state_updated_at,
		turn_opened_at = excluded.turn_opened_at, turn_settled_at = excluded.turn_settled_at, turn_snoozed_until = excluded.turn_snoozed_until,
		turn_agent_at = excluded.turn_agent_at, turn_requested_at = excluded.turn_requested_at, last_model_request_at = excluded.last_model_request_at,
		agent_driver_plugin_name = excluded.agent_driver_plugin_name, agent_driver_run_id = excluded.agent_driver_run_id,
		agent_driver_report_seq = excluded.agent_driver_report_seq, agent_metadata = excluded.agent_metadata,
		agent_driver_transcript_path = excluded.agent_driver_transcript_path, branch = excluded.branch, is_worktree = excluded.is_worktree,
		main_repo = excluded.main_repo, repository = excluded.repository, harness_title = excluded.harness_title,
		last_seen = CASE WHEN excluded.last_seen = '' THEN session_runtime.last_seen ELSE excluded.last_seen END,
		pending_switch = excluded.pending_switch`,
		id, string(state.State), string(protocol.NewTimestamp(state.Since)), string(protocol.NewTimestamp(state.Updated)),
		turnStamp(state.Turn.OpenedAt), turnStamp(state.Turn.SettledAt), turnStamp(state.Turn.SnoozedUntil), turnStamp(state.Turn.AgentAt), turnStamp(state.Turn.RequestedAt), request,
		state.Driver.PluginName, state.Driver.RunID, state.Driver.Seq, state.DriverMetadata, state.DriverTranscript,
		state.Branch.Branch, boolToInt(state.Branch.IsWorktree), state.Branch.MainRepo, state.Branch.Repository, state.HarnessTitle, lastSeen, state.PendingSwitch, id)
	if err != nil {
		log.Printf("[store] save session runtime: session=%s: %v", id, err)
		return false
	}
	_, err = result.RowsAffected()
	return err == nil
}

func turnStamp(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(sortableTimeFormat)
}

func (s *Store) SessionPendingSwitch(id protocol.SessionID) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return s.pendingSwitches[id]
	}
	var pending string
	if err := s.db.QueryRow(`SELECT pending_switch FROM session_runtime WHERE session_id = ?`, id).Scan(&pending); err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("[store] session %s pending switch: %v", id, err)
	}
	return pending
}
