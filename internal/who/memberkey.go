package who

import (
	"fmt"
	"regexp"
)

type MemberKey struct{ key string }

var memberKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

func ParseMemberKey(stored string) (MemberKey, error) {
	if !memberKeyPattern.MatchString(stored) {
		return MemberKey{}, fmt.Errorf("invalid stored member key %q: lowercase letters, digits and -, starting with a letter, at most 40 characters", stored)
	}
	return MemberKey{key: stored}, nil
}

func (k MemberKey) IsZero() bool   { return k.key == "" }
func (k MemberKey) String() string { return k.key }
