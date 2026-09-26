package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

func newTestJobStore(t *testing.T, d *Daemon) jobs.Store {
	t.Helper()
	return store.NewJobStore(d.store, t.TempDir(), func(string, ...any) {})
}
