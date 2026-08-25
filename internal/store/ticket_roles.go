package store

import (
	"database/sql"
	"errors"
	"strings"
)

const (
	TicketRoleChiefOfStaff   = "chief_of_staff"
	ticketRoleIdentityPrefix = "role:"
)

func TicketRoleIdentity(role string) string {
	role = strings.TrimSpace(role)
	if role == "" {
		return ""
	}
	return ticketRoleIdentityPrefix + role
}

func (s *Store) IsTicketRoleOwner(role, ticketID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return false, nil
	}
	var one int
	err := s.db.QueryRow(`
		SELECT 1 FROM ticket_role_owners WHERE role = ? AND ticket_id = ?
	`, strings.TrimSpace(role), strings.TrimSpace(ticketID)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) TicketAssigneesOwnedByRole(role string) map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]bool)
	if s.db == nil || strings.TrimSpace(role) == "" {
		return out
	}
	rows, err := s.db.Query(`
		SELECT DISTINCT t.assignee
		FROM tickets t
		JOIN ticket_role_owners o ON o.ticket_id = t.id
		WHERE o.role = ? AND t.assignee != '' AND t.archived_at = ''
	`, strings.TrimSpace(role))
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var assignee string
		if err := rows.Scan(&assignee); err == nil && strings.TrimSpace(assignee) != "" {
			out[strings.TrimSpace(assignee)] = true
		}
	}
	return out
}
