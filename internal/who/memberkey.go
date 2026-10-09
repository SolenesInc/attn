package who

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
)

type MemberKey struct{ key string }

var ErrNobody = errors.New("who: an empty value is never stored")
var memberKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

func ParseMemberKey(stored string) (MemberKey, error) {
	if !memberKeyPattern.MatchString(stored) {
		return MemberKey{}, fmt.Errorf("invalid stored member key %q: lowercase letters, digits and -, starting with a letter, at most 40 characters", stored)
	}
	return MemberKey{key: stored}, nil
}
func MemberKeyOfHome(homeDir string) (MemberKey, error) {
	return ParseMemberKey(filepath.Base(homeDir))
}
func (k MemberKey) IsZero() bool   { return k.key == "" }
func (k MemberKey) String() string { return k.key }
func (k MemberKey) MarshalText() ([]byte, error) {
	if k.IsZero() {
		return nil, ErrNobody
	}
	return []byte(k.key), nil
}
func (k *MemberKey) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*k = MemberKey{}
		return nil
	}
	parsed, err := ParseMemberKey(string(text))
	if err == nil {
		*k = parsed
	}
	return err
}
func (k MemberKey) Value() (driver.Value, error) {
	text, err := k.MarshalText()
	if err != nil {
		return nil, err
	}
	return string(text), nil
}
func (k *MemberKey) Scan(src any) error {
	switch value := src.(type) {
	case string:
		return k.UnmarshalText([]byte(value))
	case []byte:
		return k.UnmarshalText(value)
	case nil:
		return k.UnmarshalText(nil)
	default:
		return fmt.Errorf("who: cannot scan member key from %T", src)
	}
}
