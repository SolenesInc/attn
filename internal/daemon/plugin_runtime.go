package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/plugins"
	"github.com/victorarias/attn/internal/procreap"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/supervise"
)

const pluginManifestName = plugins.ManifestName

type pluginManifest = plugins.Manifest
type pluginManifestIssue = plugins.ManifestIssue

type pluginAvailability string

const (
	pluginAvailabilityBundled pluginAvailability = "bundled"
	pluginAvailabilityUser    pluginAvailability = "user"
)

type pluginCatalogItem struct {
	Manifest     pluginManifest
	Availability pluginAvailability
	Installed    bool
}

func discoverPluginManifests(pluginDir string) ([]pluginManifest, []pluginManifestIssue) {
	return plugins.Discover(pluginDir)
}

// Plugin discovery stays in the same runtime root as the daemon socket: app-managed restarts
// route by socket path, so an inherited ATTN_PROFILE would reach the wrong plugins.
func pluginDirForSocket(socketPath string) string {
	if override := strings.TrimSpace(os.Getenv("ATTN_PLUGIN_DIR")); override != "" {
		return override
	}
	return filepath.Join(filepath.Dir(socketPath), "plugins")
}

func bundledPluginDirForExecutable() string {
	if override := strings.TrimSpace(os.Getenv("ATTN_BUNDLED_PLUGIN_DIR")); override != "" {
		return override
	}
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	macOSDir := filepath.Dir(executable)
	contentsDir := filepath.Dir(macOSDir)
	if filepath.Base(macOSDir) != "MacOS" || filepath.Base(contentsDir) != "Contents" {
		return ""
	}
	return filepath.Join(contentsDir, "Resources", "plugins")
}

func loadPluginManifest(path string) (pluginManifest, error) {
	return plugins.LoadManifest(path)
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

// Deliberately outside the plugin discovery directory: anything under there is
// scanned for manifests.
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

// Must run before any plugin starts: a stranded runtime still holds its relay
// socket open. An unreadable record is retired too — it names no pid to signal.
func (d *Daemon) reapStrandedPluginRuntimes() {
	results := plugins.ReapRuntimeProcesses(filepath.Dir(d.socketPath))
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
	catalog, issues := d.pluginCatalog()
	d.logf("plugin discovery user_dir=%s bundled_dir=%s catalog=%d issues=%d", d.pluginDir, d.bundledPluginDir, len(catalog), len(issues))
	for _, issue := range issues {
		d.logf("plugin manifest skipped: %v", issue)
	}
	d.armPluginDriverSilenceWatchForEveryRun()
	for _, item := range catalog {
		if !item.Installed {
			continue
		}
		if err := d.startInstalledPlugin(item.Manifest); err != nil {
			d.logf("plugin %s failed to start: %v", item.Manifest.Name, err)
		}
	}
}

func (d *Daemon) pluginCatalog() ([]pluginCatalogItem, []pluginManifestIssue) {
	bundled, bundledIssues := discoverPluginManifests(d.bundledPluginDir)
	user, userIssues := discoverPluginManifests(d.pluginDir)
	installedBundled := d.installedBundledPlugins()
	userNames := make(map[string]struct{}, len(user))
	items := make([]pluginCatalogItem, 0, len(bundled)+len(user))
	for _, manifest := range user {
		userNames[manifest.Name] = struct{}{}
		items = append(items, pluginCatalogItem{Manifest: manifest, Availability: pluginAvailabilityUser, Installed: true})
	}
	for _, manifest := range bundled {
		if _, collision := userNames[manifest.Name]; collision {
			continue
		}
		_, installed := installedBundled[manifest.Name]
		items = append(items, pluginCatalogItem{Manifest: manifest, Availability: pluginAvailabilityBundled, Installed: installed})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Manifest.Name < items[j].Manifest.Name })
	issues := append([]pluginManifestIssue(nil), bundledIssues...)
	issues = append(issues, userIssues...)
	return items, issues
}

func (d *Daemon) installedBundledPlugins() map[string]struct{} {
	d.bundledPluginMu.Lock()
	defer d.bundledPluginMu.Unlock()
	d.loadInstalledBundledPluginsLocked()
	result := make(map[string]struct{}, len(d.bundledPluginSet))
	for name := range d.bundledPluginSet {
		result[name] = struct{}{}
	}
	return result
}

func (d *Daemon) loadInstalledBundledPluginsLocked() {
	if d.bundledPluginLoaded {
		return
	}
	d.bundledPluginLoaded = true
	d.bundledPluginSet = make(map[string]struct{})
	raw := strings.TrimSpace(d.store.GetSetting(SettingInstalledBundledPlugins))
	if raw == "" {
		return
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		d.logf("failed to decode installed bundled plugins: %v", err)
		return
	}
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			d.bundledPluginSet[name] = struct{}{}
		}
	}
}

func (d *Daemon) setBundledPluginInstalled(name string, installed bool) error {
	d.bundledPluginMu.Lock()
	defer d.bundledPluginMu.Unlock()
	d.loadInstalledBundledPluginsLocked()
	next := make(map[string]struct{}, len(d.bundledPluginSet)+1)
	for installedName := range d.bundledPluginSet {
		next[installedName] = struct{}{}
	}
	if installed {
		next[name] = struct{}{}
	} else {
		delete(next, name)
	}
	names := make([]string, 0, len(next))
	for installedName := range next {
		names = append(names, installedName)
	}
	sort.Strings(names)
	encoded, err := json.Marshal(names)
	if err != nil {
		return err
	}
	if err := d.store.SetSettingChecked(SettingInstalledBundledPlugins, string(encoded)); err != nil {
		return err
	}
	d.bundledPluginSet = next
	return nil
}

func (d *Daemon) startInstalledPlugin(manifest pluginManifest) error {
	return d.ensurePluginSupervisor().Ensure(manifest)
}

func (d *Daemon) pluginCommandEnv(extra ...string) []string {
	env := append([]string(nil), os.Environ()...)
	env = mergePluginEnvironment(env, d.cachedLoginShellEnv())
	// The daemon names the driver's denial-ledger path because only it knows which
	// profile's data dir it is serving; reconcileAutoModeDenialLedger reads the same.
	env = mergePluginEnvironment(env, []string{automode.DenialLedgerEnvVar + "=" + autoModeDenialLedgerPath()})
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
