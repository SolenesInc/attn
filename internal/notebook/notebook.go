package notebook

import (
	"crypto/sha256"
	"encoding/hex"
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
