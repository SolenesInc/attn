package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/supervise"
)

func TestDaemon_PluginsUpdatedMessageReportsAParkedPlugin(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.pluginDir = filepath.Join(t.TempDir(), "plugins")
	writeTestPluginManifest(t, d.pluginDir, "doomed-provider")
	manifest, err := loadPluginManifest(filepath.Join(d.pluginDir, "doomed-provider", pluginManifestName))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}

	clock := newFakePluginClock()
	launcher := &fakePluginLauncher{}
	d.pluginSupervisor = newPluginSupervisor(launcher, clock, nil, supervise.Options{GiveUpAfter: 1})
	if err := d.pluginSupervisor.Ensure(manifest); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	launcher.handle(0).exit(pluginExit{ExitCode: intPtr(1)})
	waitForSupervisor(t, func() bool {
		snapshot, _ := d.pluginSupervisor.Snapshot(manifest.Name)
		return snapshot.Phase == pluginPhaseBackoff
	})
	clock.Advance(pluginRestartBackoff[0])
	waitForSupervisor(t, func() bool { return launcher.count() == 2 })
	launcher.handle(1).exit(pluginExit{ExitCode: intPtr(1)})
	waitForSupervisor(t, func() bool {
		snapshot, _ := d.pluginSupervisor.Snapshot(manifest.Name)
		return snapshot.Phase == pluginPhaseParked
	})

	plugins := d.pluginsUpdatedMessage().Plugins
	if len(plugins) != 1 {
		t.Fatalf("plugin count=%d, want 1", len(plugins))
	}
	plugin := plugins[0]
	if got := protocol.Deref(plugin.RuntimePhase); got != string(pluginPhaseParked) {
		t.Fatalf("runtime phase=%q, want parked", got)
	}
	if plugin.RuntimeState != pluginRuntimeStateParked {
		t.Fatalf("runtime state=%q, want parked", plugin.RuntimeState)
	}
	if plugin.NextRestartAt != nil {
		t.Fatalf("next restart=%v, want nothing scheduled", plugin.NextRestartAt)
	}
}

func TestDaemon_HandleRemovePluginWSKeepsSupervisorRunningWhenDeletionFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.pluginDir = filepath.Join(t.TempDir(), "plugins")
	writeTestPluginManifest(t, d.pluginDir, "removable")
	manifest, err := loadPluginManifest(filepath.Join(d.pluginDir, "removable", pluginManifestName))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	clock := newFakePluginClock()
	launcher := &fakePluginLauncher{}
	d.pluginSupervisor = newTestPluginSupervisor(t, clock, launcher)
	if err := d.pluginSupervisor.Ensure(manifest); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	d.removePlugin = func(pluginDir, name string) error {
		return os.ErrPermission
	}

	client := &wsClient{send: make(chan outboundMessage, 1)}
	d.handleRemovePluginWS(client, &protocol.RemovePluginMessage{Name: "removable"})
	event := readOutboundEvent(t, client)
	if event["success"] != false {
		t.Fatalf("remove event=%v, want failure", event)
	}
	if _, err := os.Stat(manifest.Dir); err != nil {
		t.Fatalf("installed plugin missing after failed remove: %v", err)
	}
	snapshot, _ := d.pluginSupervisor.Snapshot("removable")
	if snapshot.Desired != pluginDesiredRunning || !snapshot.Running {
		t.Fatalf("snapshot after failed remove=%+v", snapshot)
	}
	if got := launcher.count(); got != 1 {
		t.Fatalf("start count after failed remove=%d, want 1", got)
	}
}
