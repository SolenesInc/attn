package store

import (
	"database/sql"
	"log"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
)

type TurnStamps struct {
	OpenedAt     time.Time
	SettledAt    time.Time
	SnoozedUntil time.Time
	AgentAt      time.Time
	RequestedAt  time.Time
}

type TurnOpening struct {
	Opens        bool
	BreaksSnooze bool
}

type TurnOpeningOutcome struct {
	HeldBySnooze bool
	EndedSnooze  time.Time
}

func (s *Store) SnoozedSessions() map[protocol.SessionID]time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snoozed := make(map[protocol.SessionID]time.Time)

	if s.db == nil {
		for id, stamps := range s.turnStamps {
			if !stamps.SnoozedUntil.IsZero() {
				snoozed[id] = stamps.SnoozedUntil
			}
		}
		return snoozed
	}

	rows, err := s.db.Query(`SELECT id, turn_snoozed_until FROM sessions_with_runtime WHERE turn_snoozed_until != '' AND closed_at = ''`)
	if err != nil {
		log.Printf("[store] SnoozedSessions: failed: %v", err)
		return snoozed
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id    string
			until string
		)
		if err := rows.Scan(&id, &until); err != nil {
			log.Printf("[store] SnoozedSessions: scan failed: %v", err)
			continue
		}
		if parsed := parseTurnStamp(until); !parsed.IsZero() {
			snoozed[protocol.SessionID(id)] = parsed
		}
	}
	return snoozed
}

func (s *Store) TurnStamps(id protocol.SessionID) TurnStamps {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.turnStampsLocked(id)
}

func (s *Store) turnStampsLocked(id protocol.SessionID) TurnStamps {
	if s.db == nil {
		return s.turnStamps[id]
	}

	var (
		opened    string
		settled   string
		snoozed   string
		agent     string
		requested string
	)
	err := s.db.QueryRow(
		`SELECT turn_opened_at, turn_settled_at, turn_snoozed_until, turn_agent_at, turn_requested_at FROM sessions_with_runtime WHERE id = ?`, id).
		Scan(&opened, &settled, &snoozed, &agent, &requested)
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
		AgentAt:      parseTurnStamp(agent),
		RequestedAt:  parseTurnStamp(requested),
	}
}

func (s *Store) setTurnStampsLocked(id protocol.SessionID, stamps TurnStamps) {
	if s.turnStamps == nil {
		s.turnStamps = make(map[protocol.SessionID]TurnStamps)
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
	session.TurnByAgent = nil
	if attention.TurnByAgent(stamps.OpenedAt, stamps.SettledAt, stamps.AgentAt) {
		session.TurnByAgent = protocol.Ptr(true)
	}
}

func (s *Store) SetSessionPriority(id protocol.SessionID, priority bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		session := s.sessions[id]
		if session == nil {
			return false
		}
		session.Priority = nil
		if priority {
			session.Priority = protocol.Ptr(true)
		}
		return true
	}
	result, err := s.db.Exec(`UPDATE sessions SET priority = ? WHERE id = ? AND closed_at = ''`, boolToInt(priority), id)
	if err != nil {
		log.Printf("[store] SetSessionPriority: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}
