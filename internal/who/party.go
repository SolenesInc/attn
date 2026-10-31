package who

import "github.com/victorarias/attn/internal/protocol"

type kind uint8

const (
	session kind = iota + 1
	member
	tenderOf
	user
	attn
)

type ref struct {
	kind kind
	id   string
}

type Party struct{ party ref }
type Actor struct{ actor ref }
type Address struct{ address ref }

func Member(k MemberKey) Party                { return Party{ref{member, k.String()}} }
func User() Actor                             { return Actor{ref{kind: user}} }
func Attn() Actor                             { return Actor{ref{kind: attn}} }
func ToSession(id protocol.SessionID) Address { return Address{ref{session, string(id)}} }
func ToTenderOf(seedID string) Address        { return Address{ref{tenderOf, seedID}} }

func (p Party) Actor() Actor     { return Actor{p.party} }
func (p Party) Address() Address { return Address{p.party} }

func (a Actor) Party() (Party, bool) {
	if a.actor.kind == session || a.actor.kind == member {
		return Party{a.actor}, true
	}
	return Party{}, false
}

func (p Party) Session() (protocol.SessionID, bool) {
	if p.party.kind == session {
		return protocol.SessionID(p.party.id), true
	}
	return "", false
}

func (p Party) Member() (MemberKey, bool) {
	if p.party.kind == member {
		return MemberKey{p.party.id}, true
	}
	return MemberKey{}, false
}

func (p Party) IsZero() bool   { return p.party.kind == 0 }
func (a Actor) IsZero() bool   { return a.actor.kind == 0 }
func (a Address) IsZero() bool { return a.address.kind == 0 }
