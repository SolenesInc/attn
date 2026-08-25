// The reader is permissive by contract: never reject a file for an unknown type, extra
// keys, broken links, or a missing index, and preserve unknown keys on round-trip.
package notebook

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

const TypeJournal = "journal"

const MaxFileSize = 2 << 20

func Hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

type Entry struct {
	Path    string
	Type    string
	Title   string
	Summary string
	Updated string
	Size    int64
}

type Conflict struct {
	CurrentHash string
}

type NotFoundError struct{ Path string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("notebook: %s not found", e.Path)
}

func IsNotFound(err error) bool {
	var nf *NotFoundError
	return errors.As(err, &nf)
}
