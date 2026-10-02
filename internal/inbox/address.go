package inbox

import (
	"fmt"
	"strings"
)

type addressKind uint8

const (
	sessionAddress addressKind = iota + 1
	memberAddress
	chiefAddress
)

type Address struct {
	kind addressKind
	id   string
}

func ToSession(id string) Address { return Address{sessionAddress, id} }
func ToMember(id string) Address  { return Address{memberAddress, id} }
func ToChief() Address            { return Address{chiefAddress, ""} }
func (a Address) SessionID() string {
	if a.kind == sessionAddress {
		return a.id
	}
	return ""
}
func (a Address) MemberID() string {
	if a.kind == memberAddress {
		return a.id
	}
	return ""
}
func (a Address) String() string {
	switch a.kind {
	case sessionAddress:
		return "session:" + a.id
	case memberAddress:
		return "member:" + a.id
	case chiefAddress:
		return "role:chief"
	}
	return ""
}
func ParseAddress(s string) (Address, error) {
	if s == "role:chief" {
		return ToChief(), nil
	}
	prefix, id, ok := strings.Cut(s, ":")
	if ok && id != "" {
		switch prefix {
		case "session":
			return ToSession(id), nil
		case "member":
			return ToMember(id), nil
		}
	}
	return Address{}, fmt.Errorf("invalid inbox address %q", s)
}
