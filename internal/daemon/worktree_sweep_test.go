package daemon

import (
	"path/filepath"
	"testing"
)

func sweepDaemon(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
}
