package who

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"unicode"

	"github.com/victorarias/attn/internal/protocol"
)

func parse(text string) (ref, error) {
	switch text {
	case "user":
		return ref{kind: user}, nil
	case "attn":
		return ref{kind: attn}, nil
	}
	prefix, id, ok := strings.Cut(text, ":")
	if !ok || id == "" || strings.ContainsFunc(id, unicode.IsSpace) {
		return ref{}, fmt.Errorf("invalid who reference %q", text)
	}
	switch prefix {
	case "session":
		return ref{session, id}, nil
	case "member":
		key, err := ParseMemberKey(id)
		if err != nil {
			return ref{}, err
		}
		return ref{member, key.String()}, nil
	case "seed":
		return ref{tenderOf, id}, nil
	case "chief":
		return ref{chiefOf, id}, nil
	}
	return ref{}, fmt.Errorf("invalid who reference %q", text)
}
func ParseParty(text string) (Party, error) {
	r, err := parse(text)
	if err == nil && r.kind != session && r.kind != member {
		err = fmt.Errorf("%q is not a party", text)
	}
	if err != nil {
		return Party{}, err
	}
	return Party{r}, nil
}
func ParseActor(text string) (Actor, error) {
	r, err := parse(text)
	if err == nil && (r.kind == tenderOf || r.kind == chiefOf) {
		err = fmt.Errorf("%q is not an actor", text)
	}
	if err != nil {
		return Actor{}, err
	}
	return Actor{r}, nil
}
func ParseAddress(text string) (Address, error) {
	r, err := parse(text)
	if err == nil && (r.kind == user || r.kind == attn) {
		err = fmt.Errorf("%q is not an address", text)
	}
	if err != nil {
		return Address{}, err
	}
	return Address{r}, nil
}
func (r ref) String() string {
	switch r.kind {
	case session:
		return "session:" + r.id
	case member:
		return "member:" + r.id
	case tenderOf:
		return "seed:" + r.id
	case chiefOf:
		return "chief:" + r.id
	case user:
		return "user"
	case attn:
		return "attn"
	}
	return ""
}
func (p Party) String() string             { return p.party.String() }
func (a Actor) String() string             { return a.actor.String() }
func (a Address) String() string           { return a.address.String() }
func (p Party) Ref() protocol.PartyRef     { return protocol.PartyRef(p.String()) }
func (a Actor) Ref() protocol.ActorRef     { return protocol.ActorRef(a.String()) }
func (a Address) Ref() protocol.AddressRef { return protocol.AddressRef(a.String()) }
func encode(r ref) ([]byte, error) {
	if r.kind == 0 || (r.kind != user && r.kind != attn && r.id == "") {
		return nil, ErrNobody
	}
	text := r.String()
	if _, err := parse(text); err != nil {
		return nil, err
	}
	return []byte(text), nil
}
func (p Party) MarshalText() ([]byte, error)   { return encode(p.party) }
func (a Actor) MarshalText() ([]byte, error)   { return encode(a.actor) }
func (a Address) MarshalText() ([]byte, error) { return encode(a.address) }
func (p *Party) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*p = Party{}
		return nil
	}
	v, err := ParseParty(string(text))
	if err == nil {
		*p = v
	}
	return err
}
func (a *Actor) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*a = Actor{}
		return nil
	}
	v, err := ParseActor(string(text))
	if err == nil {
		*a = v
	}
	return err
}
func (a *Address) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*a = Address{}
		return nil
	}
	v, err := ParseAddress(string(text))
	if err == nil {
		*a = v
	}
	return err
}
func value(r ref) (driver.Value, error) {
	text, err := encode(r)
	if err != nil {
		return nil, err
	}
	return string(text), nil
}
func (p Party) Value() (driver.Value, error)   { return value(p.party) }
func (a Actor) Value() (driver.Value, error)   { return value(a.actor) }
func (a Address) Value() (driver.Value, error) { return value(a.address) }
func scan(src any, unmarshal func([]byte) error) error {
	switch v := src.(type) {
	case nil:
		return unmarshal(nil)
	case string:
		return unmarshal([]byte(v))
	case []byte:
		return unmarshal(v)
	}
	return fmt.Errorf("who: cannot scan reference from %T", src)
}
func (p *Party) Scan(src any) error   { return scan(src, p.UnmarshalText) }
func (a *Actor) Scan(src any) error   { return scan(src, a.UnmarshalText) }
func (a *Address) Scan(src any) error { return scan(src, a.UnmarshalText) }
