package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

// Succession puts To in the terminal From showed: To takes From's place, process, driver run and
// Conversation; everything else stays with From. A new To gets Label and Launch; an existing one keeps its own.
type Succession struct {
	From, To     protocol.SessionID
	Label        string
	Conversation SessionConversation
	Launch       LaunchIntent
	Close        SessionClose
	KeepFrom     bool
}

// CommitSuccession opens sc.To, or reopens it when it exists, in the pane that holds terminal; the pane now
// shows it and To's other panes, whose terminals are dead, close. sc.From closes into the ledger in the same
// transaction unless KeepFrom. It returns the desktops it changed, terminal's first.
func (s *Store) CommitSuccession(sc Succession, terminal protocol.TerminalID) ([]profiles.Desktop, error) {
	var changed []profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		var open int
		err := tx.QueryRow(`SELECT 1 FROM sessions WHERE id = ? AND closed_at = ''`, sc.From).Scan(&open)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("succeed session %s: %w", sc.From, ErrSessionClosed)
		}
		if err != nil {
			return fmt.Errorf("succeed session %s: %w", sc.From, err)
		}
		if sc.Close.By.IsZero() {
			sc.Close.By = who.User()
		}
		at := time.Now().UTC().Format(time.RFC3339Nano)
		var exists int
		err = tx.QueryRow(`SELECT 1 FROM sessions WHERE id = ?`, sc.To).Scan(&exists)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			err = openSuccessorTx(tx, sc, at)
		case err == nil:
			err = takeOverTx(tx, sc, at)
		}
		if err != nil {
			return fmt.Errorf("open successor %s: %w", sc.To, err)
		}
		var desktopID string
		if err := tx.QueryRow(`SELECT desktop_id FROM desktop_panes WHERE runtime_id = ?`, terminal).Scan(&desktopID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		dead, err := removeSessionPlacement(tx, now, sc.To)
		if err != nil {
			return fmt.Errorf("close the dead panes of %s: %w", sc.To, err)
		}
		if _, err := tx.Exec(`DELETE FROM terminal_bindings WHERE session_id = ?`, sc.To); err != nil {
			return err
		}
		if err := bindTerminalTx(tx, terminal, sc.To); err != nil {
			return err
		}
		for _, desktop := range dead {
			if desktop.ID != desktopID {
				changed = append(changed, desktop)
			}
		}
		if desktopID != "" {
			desktop, err := loadDesktop(tx, desktopID)
			if err != nil {
				return err
			}
			for i := range desktop.Panes {
				if desktop.Panes[i].RuntimeID == terminal {
					desktop.Panes[i].SessionID = sc.To
				}
			}
			if err := writeCurrentDesktopArrangement(tx, now, &desktop); err != nil {
				return fmt.Errorf("show successor %s: %w", sc.To, err)
			}
			changed = append([]profiles.Desktop{desktop}, changed...)
		}
		if sc.KeepFrom {
			return nil
		}
		if _, _, err = s.closeSessionTx(tx, sc.From, sc.Close, at); err == nil {
			_, err = tx.Exec(`UPDATE sessions SET agent_driver_plugin_name = '', agent_driver_run_id = '', agent_driver_report_seq = 0,
				agent_driver_transcript_path = '' WHERE id = ?`, sc.From)
		}
		if err != nil {
			return fmt.Errorf("succeed session %s: %w", sc.From, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if sc.KeepFrom {
		return changed, nil
	}
	s.forgetSessionCost(sc.From)
	s.mu.Lock()
	delete(s.touchedAt, sc.From)
	s.mu.Unlock()
	return changed, nil
}

func openSuccessorTx(tx *sql.Tx, sc Succession, at string) error {
	launch, err := json.Marshal(sc.Launch)
	if err != nil {
		return err
	}
	cost, err := json.Marshal(SessionCostState{Initialized: true})
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO sessions (id, label, agent, directory, endpoint_id, profile_id, branch, is_worktree, main_repo, repository,
			state, state_since, state_updated_at, last_model_request_at, last_seen, launched_at, context_window_cap,
			resume_session_id, transcript_path, launch_intent, session_cost_json, succeeds, priority,
			agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq, agent_driver_transcript_path)
		SELECT ?, ?, agent, directory, endpoint_id, profile_id, branch, is_worktree, main_repo, repository,
			'idle', ?, ?, ?, ?, launched_at, context_window_cap,
			?, ?, ?, ?, id, priority,
			agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq, agent_driver_transcript_path
		FROM sessions WHERE id = ?`,
		sc.To, sc.Label,
		at, at, at, at,
		sc.Conversation.NativeID, sc.Conversation.TranscriptPath, string(launch), string(cost),
		sc.From,
	)
	return err
}

// takeOverTx brings an existing To back as Reopen would, into From's terminal. Its launch counts
// from now, so the lines its transcript already holds predate it.
func takeOverTx(tx *sql.Tx, sc Succession, at string) error {
	if _, err := tx.Exec(`
		UPDATE sessions SET closed_at = '', closed_by = '', close_reason = '',
			state = 'idle', state_since = ?, state_updated_at = ?, last_seen = ?, launched_at = ?,
			resume_session_id = ?, transcript_path = ?, succeeds = ?,
			(agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq, agent_driver_transcript_path) =
				(SELECT agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq, agent_driver_transcript_path
				FROM sessions WHERE id = ?)
		WHERE id = ?`,
		at, at, at, at,
		sc.Conversation.NativeID, sc.Conversation.TranscriptPath, sc.From,
		sc.From, sc.To,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM session_teardown_tombstones WHERE session_id = ?`, sc.To); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM session_exit_screens WHERE session_id = ?`, sc.To)
	return err
}
