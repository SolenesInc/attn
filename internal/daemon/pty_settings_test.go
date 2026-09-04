package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
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

func TestSharedPTYHostSettingDefaultAndToggle(t *testing.T) {
	probes := 0
	d, backend := newPTYSettingsDaemon(t, func(context.Context) error { probes++; return nil })
	if enabled, active := d.sharedPTYHostSettings(); enabled || active {
		t.Fatalf("default enabled=%v active=%v, want false/false", enabled, active)
	}
	for _, enabled := range []bool{true, false} {
		if err := d.setSharedPTYHostEnabled(enabled); err != nil {
			t.Fatal(err)
		}
		stored, active := d.sharedPTYHostSettings()
		if stored != enabled || active != enabled || backend.SharedForNewSessions() != enabled {
			t.Fatalf("toggle %v: stored=%v active=%v", enabled, stored, active)
		}
	}
	if probes != 1 {
		t.Fatalf("probed %d times, want enable only", probes)
	}
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

func TestSharedPTYHostSettingRejectsPersistenceFailure(t *testing.T) {
	d, backend := newPTYSettingsDaemon(t, func(context.Context) error { return nil })
	if err := d.store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.setSharedPTYHostEnabled(true); err == nil {
		t.Fatal("enable succeeded with a closed settings database")
	}
	if backend.SharedForNewSessions() {
		t.Fatal("persistence failure changed launch selection")
	}
}

func TestSharedPTYHostSettingProbeKeepsSnapshotAvailable(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	d, _ := newPTYSettingsDaemon(t, func(context.Context) error { close(started); <-release; return nil })
	done := make(chan error, 1)
	go func() { done <- d.setSharedPTYHostEnabled(true) }()
	<-started
	enabled, active := d.sharedPTYHostSettings()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if enabled || active {
		t.Fatalf("pending probe exposed enabled=%v active=%v, want false/false", enabled, active)
	}
}

func TestSharedPTYHostSettingProbeDoesNotBlockClientCommands(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	_, backend := newPTYSettingsDaemon(t, func(context.Context) error {
		close(started)
		<-release
		return errors.New("probe failed")
	})
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = backend
	client := newWorkspaceProtocolTestClient()
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})
	d.handleClientMessage(client, []byte(`{"cmd":"set_setting","key":"pty_shared_host_enabled","value":"true"}`))
	<-started
	d.handleClientMessage(client, []byte(`{"cmd":"get_settings"}`))
	var snapshot protocol.SettingsUpdatedMessage
	if err := json.Unmarshal((<-client.send).payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.ChangedKey != nil || snapshot.Settings[SettingSharedPTYHostActive] != "false" {
		t.Fatalf("snapshot while probing = %+v", snapshot)
	}
	close(release)
	var result protocol.SettingsUpdatedMessage
	if err := json.Unmarshal((<-client.send).payload, &result); err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(result.ChangedKey) != SettingSharedPTYHostEnabled || result.Success == nil || *result.Success {
		t.Fatalf("probe failure response = %+v", result)
	}
}

func TestSharedPTYHostSettingConcurrentChangesKeepSelectionConsistent(t *testing.T) {
	d, _ := newPTYSettingsDaemon(t, func(context.Context) error { return nil })
	for range 16 {
		start := make(chan struct{})
		var writes sync.WaitGroup
		for i := range 32 {
			writes.Go(func() {
				<-start
				if err := d.setSharedPTYHostEnabled(i%2 == 0); err != nil {
					t.Error(err)
				}
			})
		}
		close(start)
		writes.Wait()
		if enabled, active := d.sharedPTYHostSettings(); enabled != active {
			t.Fatalf("concurrent writes left stored=%v active=%v", enabled, active)
		}
	}
}

func TestSharedPTYHostSettingValidationAndOverride(t *testing.T) {
	d, _ := newPTYSettingsDaemon(t, func(context.Context) error { return nil })
	if err := d.validateSetting(SettingSharedPTYHostEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	if err := d.validateSetting(SettingSharedPTYHostEnabled, "maybe"); err == nil {
		t.Fatal("invalid boolean accepted")
	}
	if err := d.validateSetting(SettingSharedPTYHostActive, "true"); err == nil {
		t.Fatal("derived active status was writable")
	}
	d.ptyBackend = ptybackend.NewEmbedded(nil)
	if err := d.setSharedPTYHostEnabled(true); err == nil {
		t.Fatal("accepted setting on an explicit backend override")
	}
}
