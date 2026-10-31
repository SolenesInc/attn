package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

// Succession puts To in the terminal From showed: To takes From's place, process, driver run and
// Conversation; everything else stays with From. A new To gets Label and Launch; an existing one keeps its own.
// When Member's day is still From, the day moves to To.
type Succession struct {
	From, To     protocol.SessionID
	Label        string
	Conversation SessionConversation
	Launch       LaunchIntent
	Close        SessionClose
	KeepFrom     bool
	KeepTo       bool
	Live         map[string]bool
	Member       string
}

type SuccessionOutcome struct {
	ClosedAsks []string
	MemberDay  DocumentWriteResult
}

// CommitSuccession opens sc.To, or reopens it when it exists, in the pane that holds terminal; the pane now
// shows it and To's other panes close unless their terminals are Live. sc.From closes into the ledger in the
// same transaction unless KeepFrom, along with the asks addressed to it.
func (s *Store) CommitSuccession(sc Succession, terminal protocol.TerminalID) (SuccessionOutcome, error) {
	var outcome SuccessionOutcome
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
		case err == nil && !sc.KeepTo:
			err = takeOverTx(tx, sc, at)
		}
		if err != nil {
			return fmt.Errorf("open successor %s: %w", sc.To, err)
		}
		var desktopID string
		if err := tx.QueryRow(`SELECT desktop_id FROM desktop_panes WHERE runtime_id = ?`, terminal).Scan(&desktopID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := removeSessionTiles(tx, now, sc.To, sc.Live); err != nil {
			return fmt.Errorf("close the dead panes of %s: %w", sc.To, err)
		}
		if err := unbindDeadTerminalsTx(tx, sc.To, sc.Live); err != nil {
			return err
		}
		if err := bindTerminalTx(tx, terminal, sc.To); err != nil {
			return err
		}
		if err := routeThroughTerminalTx(tx, terminal, sc.To); err != nil {
			return err
		}
		if desktopID != "" {
			desktop, err := loadDesktop(tx, desktopID)
			if err != nil {
				return err
			}
			createdAt, err := paneCreationTimes(tx, desktopID)
			if err != nil {
				return err
			}
			switched := make(map[string]string)
			for i := range desktop.Panes {
				if desktop.Panes[i].RuntimeID == terminal {
					desktop.Panes[i].SessionID = sc.To
					switched[desktop.Panes[i].PaneID] = createdAt[desktop.Panes[i].PaneID]
				}
			}
			if err := writeCurrentArrivingArrangement(tx, now, &desktop, switched); err != nil {
				return fmt.Errorf("show successor %s: %w", sc.To, err)
			}
		}
		if sc.Member != "" {
			if outcome.MemberDay, err = moveMemberDayTx(tx, sc.Member, sc.From, sc.To); err != nil {
				return fmt.Errorf("move crew member %s's day to %s: %w", sc.Member, sc.To, err)
			}
		}
		if sc.KeepFrom {
			return nil
		}
		if _, _, outcome.ClosedAsks, err = s.closeSessionTx(tx, sc.From, sc.Close, at); err != nil {
			return fmt.Errorf("succeed session %s: %w", sc.From, err)
		}
		return nil
	})
	if err != nil {
		return SuccessionOutcome{}, err
	}
	if sc.KeepFrom {
		return outcome, nil
	}
	s.forgetSessionCost(sc.From)
	s.mu.Lock()
	delete(s.touchedAt, sc.From)
	s.mu.Unlock()
	return outcome, nil
}

func moveMemberDayTx(tx *sql.Tx, key string, from, to protocol.SessionID) (DocumentWriteResult, error) {
	schema, table, found, err := readCollectionTx(tx, crew.Namespace, crew.CollectionMembers)
	if err != nil || !found {
		return DocumentWriteResult{}, err
	}
	doc, exists, err := getDocumentWith(tx, schema.Namespace, schema.Collection, table, key)
	if err != nil || !exists {
		return DocumentWriteResult{}, err
	}
	member, err := crew.Decode(doc.Body)
	if err != nil || member.BindingSession != from {
		return DocumentWriteResult{}, err
	}
	member.BindingSession = to
	body, err := member.Encode()
	if err != nil {
		return DocumentWriteResult{}, err
	}
	write := DocumentWrite{Schema: schema, ID: key, Body: body, Expected: &doc.Rev}
	fact := DocumentChangedFact(schema.Namespace, schema.Collection, key, false)
	results, _, err := commitDocumentWritesWith(tx, []DocumentCommit{{Write: write, Fact: fact}}, []string{table}, time.Now())
	if err != nil {
		return DocumentWriteResult{}, err
	}
	return results[0], recordMemberSessionTx(tx, key, to)
}

func openSuccessorTx(tx *sql.Tx, sc Succession, at string) error {
	launch, err := json.Marshal(sc.Launch)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO sessions (id, user_label, agent, directory, profile_id, launched_at, context_window_cap,
			resume_session_id, transcript_path, launch_intent, succeeds, priority, created_at, host)
		SELECT ?, ?, agent, directory, profile_id, launched_at, context_window_cap,
			?, ?, ?, id, priority, ?, host
		FROM sessions WHERE id = ?`,
		sc.To, sc.Label,
		sc.Conversation.NativeID, sc.Conversation.TranscriptPath, string(launch), at,
		sc.From,
	)
	return err
}

// takeOverTx brings an existing To back as Reopen would, into From's terminal. Its launch counts
// from now, so the lines its transcript already holds predate it.
func takeOverTx(tx *sql.Tx, sc Succession, at string) error {
	if _, err := tx.Exec(`
		UPDATE sessions SET closed_at = '', closed_by = '', close_reason = '', launched_at = ?,
			resume_session_id = ?, transcript_path = ?, succeeds = ?
		WHERE id = ?`,
		at, sc.Conversation.NativeID, sc.Conversation.TranscriptPath, sc.From, sc.To,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM session_teardown_tombstones WHERE session_id = ?`, sc.To); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM session_exit_screens WHERE session_id = ?`, sc.To)
	return err
}

func unbindDeadTerminalsTx(tx *sql.Tx, session protocol.SessionID, live map[string]bool) error {
	rows, err := tx.Query(`SELECT terminal_id FROM terminal_bindings WHERE session_id = ?`, session)
	if err != nil {
		return err
	}
	var dead []string
	for rows.Next() {
		var terminal string
		if err := rows.Scan(&terminal); err != nil {
			rows.Close()
			return err
		}
		if !live[terminal] {
			dead = append(dead, terminal)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, terminal := range dead {
		if _, err := tx.Exec(`DELETE FROM terminal_bindings WHERE terminal_id = ?`, terminal); err != nil {
			return err
		}
	}
	return nil
}
