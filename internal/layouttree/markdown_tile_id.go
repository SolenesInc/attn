package layouttree

import (
	"crypto/sha256"
	"encoding/hex"
)

// MarkdownTileID is the id of the tile that shows the markdown file at path on machine.
func MarkdownTileID(machine, path string) string {
	sum := sha256.Sum256([]byte(machine + "\x00" + path))
	return "tile-markdown-" + hex.EncodeToString(sum[:8])
}
