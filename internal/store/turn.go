package store

import (
	"database/sql"
	"log"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type TurnStamps struct {
	OpenedAt     time.Time
	SettledAt    time.Time
	SnoozedUntil time.Time
}

type TurnOpening struct {
	Opens      bool
	EndsSnooze time.Time
}

func (s *Store) UpdateStateOpeningTurn(id, state string, opening TurnOpening) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if !s.updateStateLocked(id, state, now) {
		return false
	}
	s.openTurnLocked(id, opening, now)
	return true
}

func (s *Store) ApplyAgentDriverStateOpeningTurn(id, runID string, seq uint64, state string, requestStartedAt time.Time, opening TurnOpening) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if !s.applyAgentDriverStateLocked(id, runID, seq, state, requestStartedAt, now) {
		return false
	}
	s.openTurnLocked(id, opening, now)
	return true
}

func (s *Store) openTurnLocked(id string, opening TurnOpening, now time.Time) {
	if !opening.Opens {
		return
	}
	if !opening.EndsSnooze.IsZero() && !s.wakeTurnAtLocked(id, opening.EndsSnooze) && !s.turnStampsLocked(id).SnoozedUntil.IsZero() {
		return
	}
	s.openTurnIfClosedLocked(id, now)
}

func (s *Store) OpenTurnIfClosed(id string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openTurnIfClosedLocked(id, now)
}

func (s *Store) openTurnIfClosedLocked(id string, now time.Time) bool {
	stamp := now.UTC().Format(sortableTimeFormat)

	if s.db == nil {
		if _, ok := s.sessions[id]; !ok {
			return false
		}
		current := s.turnStamps[id]
		if current.OpenedAt.After(current.SettledAt) {
			return false
		}
		s.setTurnStampsLocked(id, TurnStamps{OpenedAt: now.UTC(), SettledAt: current.SettledAt})
		return true
	}

	result, err := s.db.Exec(`
		UPDATE sessions SET turn_opened_at = ?
		 WHERE id = ? AND closed_at = '' AND (turn_opened_at = '' OR turn_opened_at <= turn_settled_at)`,
		stamp, id)
	if err != nil {
		log.Printf("[store] OpenTurnIfClosed: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

func (s *Store) SettleTurn(id string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if _, ok := s.sessions[id]; !ok {
			return false
		}
		current := s.turnStamps[id]
		current.SettledAt = now.UTC()
		s.setTurnStampsLocked(id, current)
		return true
	}

	result, err := s.db.Exec(`UPDATE sessions SET turn_settled_at = ? WHERE id = ? AND closed_at = ''`,
		now.UTC().Format(sortableTimeFormat), id)
	if err != nil {
		log.Printf("[store] SettleTurn: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

func (s *Store) SnoozeTurn(id string, until, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if _, ok := s.sessions[id]; !ok {
			return false
		}
		current := s.turnStamps[id]
		current.SettledAt = now.UTC()
		current.SnoozedUntil = until.UTC()
		s.setTurnStampsLocked(id, current)
		return true
	}

	result, err := s.db.Exec(
		`UPDATE sessions SET turn_settled_at = ?, turn_snoozed_until = ? WHERE id = ? AND closed_at = ''`,
		now.UTC().Format(sortableTimeFormat), until.UTC().Format(sortableTimeFormat), id)
	if err != nil {
		log.Printf("[store] SnoozeTurn: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

func (s *Store) WakeTurnAtAndOpenIfClosed(id string, deadline, openedAt time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		current, ok := s.turnStamps[id]
		if !ok || current.SnoozedUntil.IsZero() || !s.sessionIsLiveLocked(id) {
			return false
		}
		if !sameTurnStamp(current.SnoozedUntil, deadline) {
			return false
		}
		current.SnoozedUntil = time.Time{}
		if !current.OpenedAt.After(current.SettledAt) {
			current.OpenedAt = openedAt.UTC()
		}
		s.setTurnStampsLocked(id, current)
		return true
	}

	result, err := s.db.Exec(
		`UPDATE sessions SET turn_snoozed_until = '', turn_opened_at = CASE
			WHEN turn_opened_at = '' OR turn_opened_at <= turn_settled_at THEN ?
			ELSE turn_opened_at END
		WHERE id = ? AND closed_at = '' AND turn_snoozed_until = ?`,
		openedAt.UTC().Format(sortableTimeFormat), id, deadline.UTC().Format(sortableTimeFormat))
	if err != nil {
		log.Printf("[store] WakeTurnAtAndOpenIfClosed: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

func (s *Store) WakeTurnAt(id string, deadline time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wakeTurnAtLocked(id, deadline)
}

func (s *Store) wakeTurnAtLocked(id string, deadline time.Time) bool {
	if s.db == nil {
		current, ok := s.turnStamps[id]
		if !ok || current.SnoozedUntil.IsZero() || !s.sessionIsLiveLocked(id) {
			return false
		}
		if !sameTurnStamp(current.SnoozedUntil, deadline) {
			return false
		}
		current.SnoozedUntil = time.Time{}
		s.setTurnStampsLocked(id, current)
		return true
	}

	result, err := s.db.Exec(`UPDATE sessions SET turn_snoozed_until = '' WHERE id = ? AND closed_at = '' AND turn_snoozed_until = ?`,
		id, deadline.UTC().Format(sortableTimeFormat))
	if err != nil {
		log.Printf("[store] clear snooze: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

func sameTurnStamp(a, b time.Time) bool {
	return a.UTC().Format(sortableTimeFormat) == b.UTC().Format(sortableTimeFormat)
}

func (s *Store) SnoozedSessions() map[string]time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snoozed := make(map[string]time.Time)

	if s.db == nil {
		for id, stamps := range s.turnStamps {
			if !stamps.SnoozedUntil.IsZero() {
				snoozed[id] = stamps.SnoozedUntil
			}
		}
		return snoozed
	}

	rows, err := s.db.Query(`SELECT id, turn_snoozed_until FROM sessions WHERE turn_snoozed_until != '' AND closed_at = ''`)
	if err != nil {
		log.Printf("[store] SnoozedSessions: failed: %v", err)
		return snoozed
	}
	defer rows.Close()
	for rows.Next() {
		var id, until string
		if err := rows.Scan(&id, &until); err != nil {
			log.Printf("[store] SnoozedSessions: scan failed: %v", err)
			continue
		}
		if parsed := parseTurnStamp(until); !parsed.IsZero() {
			snoozed[id] = parsed
		}
	}
	return snoozed
}

func (s *Store) TurnStamps(id string) TurnStamps {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.turnStampsLocked(id)
}

func (s *Store) turnStampsLocked(id string) TurnStamps {
	if s.db == nil {
		return s.turnStamps[id]
	}

	var opened, settled, snoozed string
	err := s.db.QueryRow(
		`SELECT turn_opened_at, turn_settled_at, turn_snoozed_until FROM sessions WHERE id = ?`, id).
		Scan(&opened, &settled, &snoozed)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("[store] TurnStamps: failed for session %s: %v", id, err)
		}
		return TurnStamps{}
	}
	return TurnStamps{
		OpenedAt:     parseTurnStamp(opened),
		SettledAt:    parseTurnStamp(settled),
		SnoozedUntil: parseTurnStamp(snoozed),
	}
}

func (s *Store) setTurnStampsLocked(id string, stamps TurnStamps) {
	if s.turnStamps == nil {
		s.turnStamps = make(map[string]TurnStamps)
	}
	s.turnStamps[id] = stamps
}

func parseTurnStamp(value string) time.Time { return parseStoreTime(value) }

func applyTurnStamps(session *protocol.Session, stamps TurnStamps) {
	session.TurnOpenedAt = nil
	if stamps.OpenedAt.After(stamps.SettledAt) {
		session.TurnOpenedAt = protocol.Ptr(stamps.OpenedAt.UTC().Format(time.RFC3339Nano))
	}
	session.TurnSnoozedUntil = nil
	if !stamps.SnoozedUntil.IsZero() {
		session.TurnSnoozedUntil = protocol.Ptr(stamps.SnoozedUntil.UTC().Format(time.RFC3339Nano))
	}
}
