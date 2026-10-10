package who

import "github.com/victorarias/attn/internal/protocol"

type kind uint8

const (
	session kind = iota + 1
	member
	tenderOf
	chiefOf
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
type ChiefMailbox struct{ ProfileID string }

func Member(k MemberKey) Party                        { return Party{ref{member, k.String()}} }
func User() Actor                                     { return Actor{ref{kind: user}} }
func Attn() Actor                                     { return Actor{ref{kind: attn}} }
func ToSession(id protocol.SessionID) Address         { return Address{ref{session, string(id)}} }
func ToTenderOf(id string) Address                    { return Address{ref{tenderOf, id}} }
func ToChiefOf(id string) Address                     { return Address{ref{chiefOf, id}} }
func PartyOfEndedSession(id protocol.SessionID) Party { return Party{ref{session, string(id)}} }
func (p Party) Actor() Actor                          { return Actor{p.party} }
func (p Party) Address() Address                      { return Address{p.party} }
func (a Actor) Party() (Party, bool) {
	return Party{a.actor}, a.actor.kind == session || a.actor.kind == member
}
func (p Party) Session() (protocol.SessionID, bool) {
	return protocol.SessionID(p.party.id), p.party.kind == session
}
func (p Party) Member() (MemberKey, bool) { return MemberKey{p.party.id}, p.party.kind == member }
func (p Party) IsZero() bool              { return p.party.kind == 0 }
func (a Actor) IsZero() bool              { return a.actor.kind == 0 }
func (a Address) IsZero() bool            { return a.address.kind == 0 }
func SwitchParty[T any](p Party, onSession func(protocol.SessionID) (T, error), onMember func(MemberKey) (T, error)) (T, error) {
	switch p.party.kind {
	case session:
		return onSession(protocol.SessionID(p.party.id))
	case member:
		return onMember(MemberKey{p.party.id})
	}
	var zero T
	return zero, ErrNobody
}
func SwitchAddress[T any](a Address, onSession func(protocol.SessionID) (T, error), onMember func(MemberKey) (T, error), onTender func(string) (T, error), onChief func(ChiefMailbox) (T, error)) (T, error) {
	switch a.address.kind {
	case session:
		return onSession(protocol.SessionID(a.address.id))
	case member:
		return onMember(MemberKey{a.address.id})
	case tenderOf:
		return onTender(a.address.id)
	case chiefOf:
		return onChief(ChiefMailbox{a.address.id})
	}
	var zero T
	return zero, ErrNobody
}
