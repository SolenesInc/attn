package shortid

import (
	"crypto/rand"
	"fmt"
)

const (
	BodyLen  = 6
	Alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
)

func New(prefix string) (string, error) {
	buf := make([]byte, BodyLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mint %sid: %w", prefix, err)
	}
	out := make([]byte, 0, len(prefix)+BodyLen)
	out = append(out, prefix...)
	for _, b := range buf {
		out = append(out, Alphabet[int(b)%len(Alphabet)])
	}
	return string(out), nil
}
