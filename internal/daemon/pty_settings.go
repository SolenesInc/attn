package daemon

import (
	"context"
	"fmt"
	"strconv"

	"github.com/victorarias/attn/internal/ptybackend"
)

func (d *Daemon) setSharedPTYHostEnabled(enabled bool) error {
	// Serialize probe, persistence and selection without holding any PTY IO lock.
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
