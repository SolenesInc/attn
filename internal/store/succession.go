package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/workspacelayout"
)

// Succession opens To in the terminal From showed: To takes From's place, process and driver run,
// a launch intent of its own and Conversation; everything else stays with From.
type Succession struct {
	From, To     string
	Label        string
	Conversation SessionConversation
	Launch       LaunchIntent
	Close        SessionClose
}

// CommitSuccession opens sc.To, saves layout (whose pane now shows it), and closes sc.From into the
// ledger in one transaction. A From never prompted is removed instead, and removed says so.
func (s *Store) CommitSuccession(sc Succession, layout workspacelayout.WorkspaceLayout, now time.Time) (removed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, errors.New("a succession needs the database")
	}
	launch, err := json.Marshal(sc.Launch)
	if err != nil {
		return false, err
	}
	cost, err := json.Marshal(SessionCostState{Initialized: true})
	if err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var promptedAt string
	err = tx.QueryRow(`SELECT prompted_at FROM sessions WHERE id = ? AND closed_at = ''`, sc.From).Scan(&promptedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("succeed session %s: %w", sc.From, ErrSessionClosed)
	}
	if err != nil {
		return false, fmt.Errorf("succeed session %s: %w", sc.From, err)
	}
	removed = promptedAt == ""
	if strings.TrimSpace(sc.Close.By) == "" {
		sc.Close.By = SessionClosedByUser
	}
	at := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`
		INSERT INTO sessions (id, label, agent, directory, endpoint_id, workspace_id, branch, is_worktree, main_repo, repository,
			state, state_since, state_updated_at, last_model_request_at, last_seen, launched_at, context_window_cap,
			resume_session_id, transcript_path, launch_intent, session_cost_json, succeeds,
			agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq, agent_driver_transcript_path)
		SELECT ?, ?, agent, directory, endpoint_id, ?, branch, is_worktree, main_repo, repository,
			'idle', ?, ?, ?, ?, launched_at, context_window_cap,
			?, ?, ?, ?, CASE WHEN ? THEN succeeds ELSE id END,
			agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq, agent_driver_transcript_path
		FROM sessions WHERE id = ?`,
		sc.To, sc.Label, layout.WorkspaceID,
		at, at, at, at,
		sc.Conversation.NativeID, sc.Conversation.TranscriptPath, string(launch), string(cost), removed,
		sc.From,
	); err != nil {
		return false, fmt.Errorf("open successor %s: %w", sc.To, err)
	}
	if err := saveWorkspaceLayoutTx(tx, layout); err != nil {
		return false, fmt.Errorf("show successor %s: %w", sc.To, err)
	}
	if removed {
		err = deleteSessionRows(tx, sc.From)
	} else if _, err = s.closeSessionTx(tx, sc.From, sc.Close, at); err == nil {
		_, err = tx.Exec(`UPDATE sessions SET agent_driver_plugin_name = '', agent_driver_run_id = '', agent_driver_report_seq = 0,
			agent_driver_transcript_path = '' WHERE id = ?`, sc.From)
	}
	if err != nil {
		return false, fmt.Errorf("succeed session %s: %w", sc.From, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("succeed session %s: %w", sc.From, err)
	}
	s.forgetSessionCost(sc.From)
	delete(s.touchedAt, sc.From)
	return removed, nil
}
