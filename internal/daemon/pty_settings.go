package daemon

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/ptybackend"
)

func (d *Daemon) setSharedPTYHostEnabled(enabled bool) error {
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
			return fmt.Errorf("cannot enable shared PTY host: %w", err)
		}
	}
	d.ptySettingsMu.Lock()
	defer d.ptySettingsMu.Unlock()
	if err := d.store.SetSettingChecked(SettingSharedPTYHostEnabled, strconv.FormatBool(enabled)); err != nil {
		return fmt.Errorf("save shared PTY host setting: %w", err)
	}
	backend.SetSharedForNewSessions(enabled)
	return nil
}

func (d *Daemon) sharedPTYHostSettings() (enabled, active bool) {
	d.ptySettingsMu.Lock()
	defer d.ptySettingsMu.Unlock()
	enabled = parseBooleanSetting(d.store.GetSetting(SettingSharedPTYHostEnabled))
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

func (d *Daemon) handleSharedArtifactRejected(rejection ptybackend.SharedArtifactRejection) {
	outcome := "Shared terminals are unavailable until a working host is installed."
	if rejection.FallbackID != "" {
		outcome = "New terminals keep using the last-known-good shared host."
	}
	d.addWarning(warnPTYHostArtifactRejected, fmt.Sprintf(
		"Shared PTY host %s (%s) failed validation: %s. %s",
		rejection.Source, rejection.ArtifactID, rejection.Reason, outcome,
	))
}

func (d *Daemon) validateSharedPTYHostAfterRecovery() {
	host := d.sharedPTYHost
	if host == nil || !shouldRunWorkerStartupProbe() || !host.SharedCandidatePending() {
		return
	}
	migrating, routed := d.ptyBackend.(*ptybackend.MigratingBackend)
	if routed && !parseBooleanSetting(d.store.GetSetting(SettingSharedPTYHostEnabled)) {
		return
	}
	ctx, cancel := context.WithTimeout(d.doneContext(), workerStartupProbeTimeout)
	err := host.ValidateSharedCandidate(ctx, false)
	cancel()
	if err != nil {
		d.logf("shared PTY host candidate validation: %v", err)
	}
	if !routed {
		return
	}
	d.ptySettingsChangeMu.Lock()
	defer d.ptySettingsChangeMu.Unlock()
	d.ptySettingsMu.Lock()
	enabled := parseBooleanSetting(d.store.GetSetting(SettingSharedPTYHostEnabled))
	active := enabled && host.SharedArtifactReady()
	changed := migrating.SharedForNewSessions() != active
	migrating.SetSharedForNewSessions(active)
	d.ptySettingsMu.Unlock()
	if changed {
		d.publishSettingsFact(FactSettingChanged, SettingSharedPTYHostActive)
	}
}
