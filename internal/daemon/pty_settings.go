package daemon

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) setSharedPTYHostEnabled(value string) error {
	enabled := parseBooleanSetting(setting.SharedPTYHostEnabled.WithDefault(value))
	d.ptySettingsChangeMu.Lock()
	defer d.ptySettingsChangeMu.Unlock()
	backend, ok := d.ptyBackend.(*ptybackend.MigratingBackend)
	if !ok {
		return fmt.Errorf("shared PTY host setting is unavailable with backend %q", d.ptyBackendMode())
	}
	if enabled {
		ctx, cancel := context.WithTimeout(context.Background(), workerStartupProbeTimeout)
		defer cancel()
		if err := backend.ProbeShared(ctx); err != nil {
			if value != "" {
				return fmt.Errorf("cannot enable shared PTY host: %w", err)
			}
			d.logf("shared PTY host unavailable after a reset: %v; new sessions remain on legacy workers", err)
			enabled = false
		}
	}
	d.ptySettingsMu.Lock()
	defer d.ptySettingsMu.Unlock()
	if err := d.store.SetSettingChecked(setting.SharedPTYHostEnabled.Key, value); err != nil {
		return fmt.Errorf("save shared PTY host setting: %w", err)
	}
	backend.SetSharedForNewSessions(enabled)
	return nil
}

func (d *Daemon) followSharedPTYHost() {
	d.ptySettingsChangeMu.Lock()
	defer d.ptySettingsChangeMu.Unlock()
	backend, ok := d.ptyBackend.(*ptybackend.MigratingBackend)
	if !ok {
		return
	}
	enabled := d.on(setting.SharedPTYHostEnabled)
	if enabled {
		ctx, cancel := context.WithTimeout(d.life.Context(), workerStartupProbeTimeout)
		defer cancel()
		if err := backend.ProbeShared(ctx); err != nil {
			d.logf("shared PTY host unavailable: %v; new sessions remain on legacy workers", err)
			enabled = false
		}
	}
	d.ptySettingsMu.Lock()
	backend.SetSharedForNewSessions(enabled)
	d.ptySettingsMu.Unlock()
	d.publishFact(FactSettingChanged, setting.SharedPTYHostActive.Key, nil)
}

func (d *Daemon) sharedPTYHostSettings() (enabled, active bool) {
	d.ptySettingsMu.Lock()
	defer d.ptySettingsMu.Unlock()
	enabled = d.on(setting.SharedPTYHostEnabled)
	if backend, ok := d.ptyBackend.(*ptybackend.MigratingBackend); ok {
		active = backend.SharedForNewSessions()
	} else {
		active = d.ptyBackendMode() == "shared"
	}
	return enabled, active
}

func (d *Daemon) newSharedPTYHost() (*ptybackend.WorkerBackend, error) {
	return ptybackend.NewSharedHost(ptybackend.WorkerBackendConfig{
		DataRoot:                 d.dataRoot,
		DaemonInstanceID:         d.daemonInstanceID,
		BinaryPath:               strings.TrimSpace(os.Getenv("ATTN_PTY_HOST_BINARY")),
		Logf:                     d.logf,
		OnTerminalBuild:          d.handleTerminalBuildChanged,
		OnSharedArtifactRejected: d.handleSharedArtifactRejected,
	})
}

const notificationKindPTYHostRejected = "pty_host_rejected"

func (d *Daemon) handleSharedArtifactRejected(rejection ptybackend.SharedArtifactRejection) error {
	impact := "New terminals keep using dedicated workers until a working shared host is installed."
	if rejection.FallbackID != "" {
		impact = "New terminals keep using the last shared host build that passed."
	}
	if d.store == nil {
		return nil
	}
	record, created, err := d.store.EnsureNotification(store.NotificationRecord{
		ID:         notificationKindPTYHostRejected + ":" + rejection.ArtifactID,
		Kind:       notificationKindPTYHostRejected,
		Severity:   store.NotificationWarning,
		Title:      "Shared PTY host update failed its check",
		Body:       impact,
		Detail:     rejection.Source,
		Trigger:    "attn checked a newly installed shared PTY host with a throwaway terminal.",
		Impact:     impact,
		Cause:      rejection.Reason,
		SourceKind: "pty_host",
		SourceID:   rejection.ArtifactID,
	}, time.Now())
	if err != nil || !created {
		return err
	}
	d.publishFact(FactNotificationCreated, record.ID, nil)
	return nil
}

func (d *Daemon) validateSharedPTYHostAfterRecovery() {
	host := d.sharedPTYHost
	if host == nil || !shouldRunWorkerStartupProbe() {
		return
	}
	migrating, routed := d.ptyBackend.(*ptybackend.MigratingBackend)
	if routed && !d.on(setting.SharedPTYHostEnabled) {
		return
	}
	ctx, cancel := context.WithTimeout(d.life.Context(), workerStartupProbeTimeout)
	if err := host.ValidateSharedCandidate(ctx, false); err != nil {
		d.logf("shared PTY host candidate validation: %v", err)
	}
	cancel()
	if !routed {
		return
	}
	d.ptySettingsChangeMu.Lock()
	defer d.ptySettingsChangeMu.Unlock()
	d.ptySettingsMu.Lock()
	enabled := d.on(setting.SharedPTYHostEnabled)
	active := enabled && host.SharedArtifactReady()
	changed := migrating.SharedForNewSessions() != active
	migrating.SetSharedForNewSessions(active)
	d.ptySettingsMu.Unlock()
	if changed {
		d.publishFact(FactSettingChanged, setting.SharedPTYHostActive.Key, nil)
	}
}
