package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

type settingsProbeBackend struct {
	ptybackend.Backend
	probe func(context.Context) error
}

func (b *settingsProbeBackend) Probe(ctx context.Context) error { return b.probe(ctx) }

func newPTYSettingsDaemon(t *testing.T, probe func(context.Context) error) (*Daemon, *ptybackend.MigratingBackend) {
	t.Helper()
	backend, err := ptybackend.NewMigrating(ptybackend.NewEmbedded(nil), &settingsProbeBackend{probe: probe}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := store.New()
	t.Cleanup(func() { _ = s.Close() })
	return &Daemon{store: s, ptyBackend: backend}, backend
}

func TestSharedPTYHostSettingRejectsProbeFailure(t *testing.T) {
	d, backend := newPTYSettingsDaemon(t, func(context.Context) error { return errors.New("host unavailable") })
	if err := d.setSharedPTYHostEnabled(true); err == nil || !strings.Contains(err.Error(), "host unavailable") {
		t.Fatalf("enable error = %v", err)
	}
	if d.store.GetSetting(SettingSharedPTYHostEnabled) != "" || backend.SharedForNewSessions() {
		t.Fatal("failed probe changed the setting or launch selection")
	}
	d.store.SetSetting(SettingSharedPTYHostEnabled, "true")
	if enabled, active := d.sharedPTYHostSettings(); !enabled || active {
		t.Fatalf("startup fallback enabled=%v active=%v, want true/false", enabled, active)
	}
}
