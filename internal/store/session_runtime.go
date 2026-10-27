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
	PullRequests       string
}

func (s *Store) SaveSessionRuntime(id protocol.SessionID, state SessionRuntimeState) bool {
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
		if !state.LastSeen.IsZero() {
			session.LastSeen = string(protocol.NewTimestamp(state.LastSeen))
		}
		s.setTurnStampsLocked(id, state.Turn)
		if s.agentDriverRuns == nil {
			s.agentDriverRuns = make(map[protocol.SessionID]AgentDriverReportCursor)
		}
		s.agentDriverRuns[id] = state.Driver
		s.agentMetadata[id] = state.DriverMetadata
		if s.agentDriverTranscripts == nil {
			s.agentDriverTranscripts = make(map[protocol.SessionID]string)
		}
		s.agentDriverTranscripts[id] = state.DriverTranscript
		if s.runtimeRecords == nil {
			s.runtimeRecords = make(map[protocol.SessionID]SessionRuntimeRecords)
		}
		records := s.runtimeRecords[id]
		records.PullRequests = state.PullRequests
		s.runtimeRecords[id] = records
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
		branch, is_worktree, main_repo, repository, harness_title, last_seen, pull_requests)
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
		pull_requests = excluded.pull_requests`,
		id, string(state.State), string(protocol.NewTimestamp(state.Since)), string(protocol.NewTimestamp(state.Updated)),
		turnStamp(state.Turn.OpenedAt), turnStamp(state.Turn.SettledAt), turnStamp(state.Turn.SnoozedUntil), turnStamp(state.Turn.AgentAt), turnStamp(state.Turn.RequestedAt), request,
		state.Driver.PluginName, state.Driver.RunID, state.Driver.Seq, state.DriverMetadata, state.DriverTranscript,
		state.Branch.Branch, boolToInt(state.Branch.IsWorktree), state.Branch.MainRepo, state.Branch.Repository, state.HarnessTitle, lastSeen, state.PullRequests, id)
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

type SessionRuntimeRecords struct {
	PendingSwitch string
	PullRequests  string
}

func (s *Store) SessionRuntimeRecords(id protocol.SessionID) SessionRuntimeRecords {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return s.runtimeRecords[id]
	}
	var records SessionRuntimeRecords
	if err := s.db.QueryRow(`SELECT pending_switch, pull_requests FROM session_runtime WHERE session_id = ?`, id).Scan(&records.PendingSwitch, &records.PullRequests); err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("[store] session %s runtime records: %v", id, err)
	}
	return records
}

func (s *Store) HasSessionRuntime(id protocol.SessionID) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		_, ok := s.runtimeRecords[id]
		return ok
	}
	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM session_runtime WHERE session_id = ?)`, id).Scan(&exists); err != nil {
		log.Printf("[store] session %s runtime existence: %v", id, err)
	}
	return exists
}

func (s *Store) SaveSessionPendingSwitches(pending map[protocol.SessionID]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		if s.runtimeRecords == nil {
			s.runtimeRecords = make(map[protocol.SessionID]SessionRuntimeRecords)
		}
		for id, chain := range pending {
			saved := s.runtimeRecords[id]
			saved.PendingSwitch = chain
			s.runtimeRecords[id] = saved
		}
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id, chain := range pending {
		if _, err := tx.Exec(`INSERT INTO session_runtime(session_id, pending_switch) VALUES (?, ?) ON CONFLICT(session_id) DO UPDATE SET pending_switch = excluded.pending_switch`, id, chain); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) PendingSessionSwitches() map[protocol.SessionID]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pending := make(map[protocol.SessionID]string)
	if s.db == nil {
		for id, records := range s.runtimeRecords {
			if records.PendingSwitch != "" {
				pending[id] = records.PendingSwitch
			}
		}
		return pending
	}
	rows, err := s.db.Query(`SELECT session_id, pending_switch FROM session_runtime WHERE pending_switch != ''`)
	if err != nil {
		log.Printf("[store] pending switches: %v", err)
		return pending
	}
	defer rows.Close()
	for rows.Next() {
		var id protocol.SessionID
		var chain string
		if err := rows.Scan(&id, &chain); err != nil {
			log.Printf("[store] pending switch: %v", err)
			continue
		}
		pending[id] = chain
	}
	return pending
}
