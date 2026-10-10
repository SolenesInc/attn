package who

import (
	"github.com/victorarias/attn/internal/protocol"
)

type SessionFacts func(protocol.SessionID) (profileID string, lasts bool)
type Bindings struct {
	sessions   SessionFacts
	memberIn   map[protocol.SessionID]MemberKey
	sessionOf  map[MemberKey]protocol.SessionID
	unreadable map[MemberKey]error
}

func NewBindings(s SessionFacts, bound map[MemberKey]protocol.SessionID, unreadable map[MemberKey]error) Bindings {
	b := Bindings{sessions: s, memberIn: make(map[protocol.SessionID]MemberKey), sessionOf: make(map[MemberKey]protocol.SessionID), unreadable: unreadable}
	for k, id := range bound {
		if _, lasts := s(id); lasts {
			b.memberIn[id] = k
			b.sessionOf[k] = id
		}
	}
	return b
}

type UnreadableMemberError struct {
	Key   MemberKey
	Cause error
}

func (e *UnreadableMemberError) Error() string { return e.Cause.Error() }
func (e *UnreadableMemberError) Unwrap() error { return e.Cause }

func (b Bindings) Check(p Party) error {
	key, member := p.Member()
	if id, session := p.Session(); session {
		key, member = b.memberIn[id]
	}
	if member && b.unreadable[key] != nil {
		return &UnreadableMemberError{Key: key, Cause: b.unreadable[key]}
	}
	return nil
}
func (b Bindings) CheckSession(id protocol.SessionID) error {
	p, _ := b.PartyOf(id)
	return b.Check(p)
}
func (b Bindings) PartyOf(id protocol.SessionID) (Party, bool) {
	if _, lasts := b.sessions(id); !lasts {
		return Party{}, false
	}
	if k, ok := b.memberIn[id]; ok {
		return Member(k), true
	}
	return Party{ref{session, string(id)}}, true
}
func (b Bindings) Lasts(p Party) bool {
	if id, ok := p.Session(); ok {
		_, lasts := b.sessions(id)
		return lasts
	}
	k, ok := p.Member()
	return ok && !k.IsZero()
}
func (b Bindings) SessionOf(p Party) (protocol.SessionID, bool) {
	if id, ok := p.Session(); ok {
		return id, b.Lasts(p)
	}
	if k, ok := p.Member(); ok {
		id, ok := b.sessionOf[k]
		return id, ok
	}
	return "", false
}
func (b Bindings) AddressesOf(id protocol.SessionID) []Address {
	a := []Address{ToSession(id)}
	if k, ok := b.memberIn[id]; ok && b.unreadable[k] == nil {
		a = append(a, Member(k).Address())
	}
	return a
}
func (b Bindings) RequestFrom(id protocol.SessionID) (Requester, bool) {
	profile, lasts := b.sessions(id)
	if !lasts {
		return Requester{}, false
	}
	p, ok := b.PartyOf(id)
	return Requester{profile, id, p.Actor()}, ok
}
