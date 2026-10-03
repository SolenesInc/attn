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
	seedAddress
)

type Address struct {
	kind addressKind
	id   string
}

func ToSession(id string) Address      { return Address{sessionAddress, id} }
func ToMember(id string) Address       { return Address{memberAddress, id} }
func ToChief(profileID string) Address { return Address{chiefAddress, profileID} }
func ToSeed(id string) Address         { return Address{seedAddress, id} }
func (a Address) SeedID() string {
	if a.kind == seedAddress {
		return a.id
	}
	return ""
}
func (a Address) SessionID() string {
	if a.kind == sessionAddress {
		return a.id
	}
	return ""
}
func (a Address) ChiefProfileID() string {
	if a.kind == chiefAddress {
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
		return "chief:" + a.id
	case seedAddress:
		return "seed:" + a.id
	}
	return ""
}
func ParseAddress(s string) (Address, error) {
	prefix, id, ok := strings.Cut(s, ":")
	if ok && id != "" {
		switch prefix {
		case "session":
			return ToSession(id), nil
		case "member":
			return ToMember(id), nil
		case "seed":
			return ToSeed(id), nil
		case "chief":
			return ToChief(id), nil
		}
	}
	return Address{}, fmt.Errorf("invalid inbox address %q", s)
}
