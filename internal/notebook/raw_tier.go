package notebook

import "path/filepath"

// Raw tier machine paths, under <root>/.attn/raw/. List, the watcher and CleanPath skip
// or reject dotdir segments, so callers write it with direct I/O, never notebook.Store.
const (
	rawDir                 = "raw"
	rawContextSnapshotsDir = "context-snapshots"
	rawSessionsDir         = "sessions"
)

func RawDir(root string) string {
	return filepath.Join(root, machineDir, rawDir)
}

func RawContextSnapshotsDir(root string) string {
	return filepath.Join(RawDir(root), rawContextSnapshotsDir)
}

func RawSessionsDir(root string) string {
	return filepath.Join(RawDir(root), rawSessionsDir)
}
