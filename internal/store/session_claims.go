package store

import (
	"encoding/json"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

type SessionClaim struct {
	ID           protocol.SessionID
	Open         bool
	Reserved     bool
	LaunchIntent bool
	Host         string
	RuntimeLive  bool
	Label, Title string
}

func (s *Store) SessionClaims() ([]SessionClaim, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		claims := make([]SessionClaim, 0, len(s.sessions))
		for id, session := range s.sessions {
			claims = append(claims, SessionClaim{ID: id, Open: true, Host: protocol.Deref(session.Host), RuntimeLive: s.runtimeRecords[id].Live, Label: session.Label, Title: protocol.Deref(session.Title)})
		}
		return claims, nil
	}
	rows, err := s.db.Query(`SELECT s.id, s.user_label, COALESCE(r.harness_title, ''), s.agent, s.directory, s.launch_intent, s.host, s.copied_from != '', COALESCE(r.live, 0)
		FROM sessions s LEFT JOIN session_runtime r ON r.session_id = s.id WHERE s.closed_at = ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[protocol.SessionID]int{}
	var claims []SessionClaim
	for rows.Next() {
		var claim SessionClaim
		var labels SessionLabels
		var agent protocol.SessionAgent
		var directory, intent string
		var copied bool
		if err := rows.Scan(&claim.ID, &labels.UserLabel, &labels.HarnessTitle, &agent, &directory, &intent, &claim.Host, &copied, &claim.RuntimeLive); err != nil {
			return nil, err
		}
		claim.Open = true
		claim.LaunchIntent = decodesLaunchIntent(intent)
		if copied {
			claim.Host = ""
		}
		claim.Label = labels.shown(claim.ID, directory, agent)
		byID[claim.ID] = len(claims)
		claims = append(claims, claim)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	reserved, err := s.db.Query(`SELECT DISTINCT session_id FROM delegation_operations WHERE session_id != '' AND state IN ('accepted', 'preparing')`)
	if err != nil {
		return nil, err
	}
	defer reserved.Close()
	for reserved.Next() {
		var id protocol.SessionID
		if err := reserved.Scan(&id); err != nil {
			return nil, err
		}
		if i, open := byID[id]; open {
			claims[i].Reserved = true
			continue
		}
		claims = append(claims, SessionClaim{ID: id, Reserved: true})
	}
	return claims, reserved.Err()
}

func decodesLaunchIntent(stored string) bool {
	if strings.TrimSpace(stored) == "" {
		return false
	}
	var intent LaunchIntent
	return json.Unmarshal([]byte(stored), &intent) == nil
}
