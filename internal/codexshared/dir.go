package codexshared

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

func SocketDir(dataDir string) string {
	sum := sha256.Sum256([]byte(dataDir))
	return filepath.Join("/tmp", "attn-cx-"+hex.EncodeToString(sum[:8]))
}
