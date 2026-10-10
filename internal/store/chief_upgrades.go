package store

import (
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

type ChiefUpgrade struct {
	ProfileID string
	Chief     who.MemberKey
	Previous  protocol.SessionID
	Agent     string
	Model     string
	Effort    string
	CWD       string
}

func (s *Store) ChiefUpgrades() ([]ChiefUpgrade, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []ChiefUpgrade{}
	if s.db == nil {
		return out, nil
	}
	rows, err := s.db.Query("SELECT profile_id,chief_member,previous_session,agent,model,effort,cwd FROM chief_upgrades ORDER BY profile_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u ChiefUpgrade
		if err := rows.Scan(&u.ProfileID, &u.Chief, &u.Previous, &u.Agent, &u.Model, &u.Effort, &u.CWD); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) FinishChiefUpgrade(key who.MemberKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM chief_upgrades WHERE chief_member = ?", key)
	return err
}

func (s *Store) MemberEverBound(key who.MemberKey) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		for _, k := range s.memberSessions {
			if k == key {
				return true, nil
			}
		}
		return false, nil
	}
	var bound bool
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM sessions WHERE member_key = ?)", key).Scan(&bound)
	return bound, err
}
