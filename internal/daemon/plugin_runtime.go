package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/plugins"
	"github.com/victorarias/attn/internal/procreap"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/supervise"
)

type pluginManifest = plugins.Manifest
type pluginManifestIssue = plugins.ManifestIssue

func discoverPluginManifests(pluginDir string) ([]pluginManifest, []pluginManifestIssue) {
	return plugins.Discover(pluginDir)
}

func pluginDirForSocket(socketPath string) string {
	if override := strings.TrimSpace(os.Getenv("ATTN_PLUGIN_DIR")); override != "" {
		return override
	}
	return filepath.Join(filepath.Dir(socketPath), "plugins")
}

func (d *Daemon) ensurePluginSupervisor() *pluginSupervisor {
	d.pluginSupervisorMu.Lock()
	defer d.pluginSupervisorMu.Unlock()
	if d.pluginSupervisor == nil {
		d.pluginSupervisor = newPluginSupervisor(
			execPluginProcessLauncher{registryDir: plugins.RuntimeRegistryDir(filepath.Dir(d.socketPath))},
			nil,
			func(manifest pluginManifest, generation uint64) []string {
				overrides := []string{
					"ATTN_SOCKET_PATH=" + d.socketPath,
					"ATTN_PLUGIN_NAME=" + manifest.Name,
					"ATTN_PLUGIN_GENERATION=" + strconv.FormatUint(generation, 10),
					"ATTN_PLUGIN_ENTRYPOINT_KIND=" + string(manifest.Plugin.Kind),
					"ATTN_PLUGIN_ROOT=" + manifest.Dir,
					"ATTN_PLUGIN_DATA_ROOT=" + pluginDataDirForSocket(d.socketPath, manifest.Name),
				}
				return d.pluginCommandEnv(overrides...)
			},
			supervise.Options{
				LogDir: pluginLogDirForSocket(d.socketPath),
				OnChange: func(pluginName string) {
					d.publishFact(FactPluginHealthChanged, pluginName, nil)
				},
				OnGiveUp: d.notifyPluginParked,
				Logf:     d.logf,
			},
		)
	}
	return d.pluginSupervisor
}

func pluginLogDirForSocket(socketPath string) string {
	return filepath.Join(filepath.Dir(socketPath), "plugin-log")
}

const notificationKindPluginParked = "plugin_parked"

func (d *Daemon) notifyPluginParked(name string, snapshot pluginRuntimeSnapshot) {
	detail := ""
	if snapshot.LastExit != nil {
		detail = snapshot.LastExit.String()
	}
	d.logf("plugin %s parked after %d restarts without a stable connection: %s", name, snapshot.RestartAttempt, detail)
	if d.store == nil {
		return
	}
	record, err := d.store.AddNotification(store.NotificationRecord{
		Kind:       notificationKindPluginParked,
		Severity:   store.NotificationCritical,
		Title:      fmt.Sprintf("Plugin stopped: %s", name),
		Body:       fmt.Sprintf("attn restarted it %d times without it ever staying up, and has stopped trying. Reinstall the plugin, or restart attn, to try again.", snapshot.RestartAttempt),
		Detail:     detail,
		SourceKind: "plugin",
		SourceID:   name,
	}, time.Now())
	if err != nil {
		d.logf("notifications: add plugin-parked notification for %s: %v", name, err)
		return
	}
	d.publishFact(FactNotificationCreated, record.ID, nil)
}

func pluginDataDirForSocket(socketPath, pluginName string) string {
	return filepath.Join(filepath.Dir(socketPath), "plugin-data", pluginName)
}

func (d *Daemon) reapStrandedPluginRuntimes() {
	results := plugins.ReapRuntimeProcesses(nil, filepath.Dir(d.socketPath))
	if len(results) == 0 {
		return
	}
	counts := map[procreap.ReapOutcome]int{}
	for _, result := range results {
		counts[result.Outcome]++
		if result.Outcome == procreap.ReapSurvived || result.Path == "" {
			continue
		}
		if err := procreap.RemoveEntry(result.Path); err != nil {
			d.logf("plugin runtime reap: retire %s: %v", result.Path, err)
		}
	}
	d.logf("plugin runtime reap: entries=%d terminated=%d killed=%d already_gone=%d unidentified=%d survived=%d unreadable=%d",
		len(results),
		counts[procreap.ReapTerminated],
		counts[procreap.ReapKilled],
		counts[procreap.ReapAlreadyGone],
		counts[procreap.ReapUnidentified],
		counts[procreap.ReapSurvived],
		counts[procreap.ReapUnreadable],
	)
}

func (d *Daemon) startInstalledPlugins() {
	d.reapStrandedPluginRuntimes()
	catalog, issues := discoverPluginManifests(d.pluginDir)
	d.logf("plugin discovery user_dir=%s catalog=%d issues=%d", d.pluginDir, len(catalog), len(issues))
	for _, issue := range issues {
		d.logf("plugin manifest skipped: %v", issue)
	}
	d.armPluginDriverSilenceWatchForEveryRun()
	for _, manifest := range catalog {
		if err := d.startInstalledPlugin(manifest); err != nil {
			d.logf("plugin %s failed to start: %v", manifest.Name, err)
		}
	}
}

func (d *Daemon) startInstalledPlugin(manifest pluginManifest) error {
	return d.ensurePluginSupervisor().Ensure(manifest)
}

func (d *Daemon) pluginCommandEnv(extra ...string) []string {
	env := append([]string(nil), os.Environ()...)
	env = mergePluginEnvironment(env, d.cachedLoginShellEnv())
	env = mergePluginEnvironment(env, extra)
	return env
}

func mergePluginEnvironment(base, overlay []string) []string {
	if len(overlay) == 0 {
		return append([]string(nil), base...)
	}

	merged := make([]string, 0, len(base)+len(overlay))
	index := make(map[string]int, len(base)+len(overlay))
	add := func(entry string) {
		key := entry
		if split := strings.Index(entry, "="); split >= 0 {
			key = entry[:split]
		}
		if pos, ok := index[key]; ok {
			merged[pos] = entry
			return
		}
		index[key] = len(merged)
		merged = append(merged, entry)
	}
	for _, entry := range base {
		add(entry)
	}
	for _, entry := range overlay {
		add(entry)
	}
	return merged
}

func (d *Daemon) stopInstalledPlugins() {
	d.ensurePluginSupervisor().Shutdown()
}

func (d *Daemon) stopInstalledPlugin(name string) {
	d.ensurePluginSupervisor().Stop(name)
}
