package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/plugins"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

const (
	pluginRuntimeStateStopped   = "stopped"
	pluginRuntimeStateStarting  = "starting"
	pluginRuntimeStateConnected = "connected"
	pluginRuntimeStateDegraded  = "degraded"
	pluginRuntimeStateParked    = "parked"
)

func (d *Daemon) handleListPluginsWS(client *wsClient) {
	d.sendToClient(client, d.pluginsUpdatedMessage())
}

func (d *Daemon) handleInstallPluginWS(client *wsClient, msg *protocol.InstallPluginMessage) {
	d.pluginActionMu.Lock()
	defer d.pluginActionMu.Unlock()
	action := "install"
	link := msg.Link != nil && *msg.Link
	if link {
		action = "link"
	}
	source := strings.TrimSpace(msg.Source)
	if source == "" {
		d.sendPluginActionResult(client, action, "", false, "plugin source is required")
		return
	}
	var manifest plugins.Manifest
	var swap *plugins.SwapHandle
	var err error
	if link {
		manifest, swap, err = plugins.ReplaceLinkPath(source, d.pluginDir, d.pluginInstallOptions())
	} else {
		manifest, swap, err = plugins.ReplaceSourceWithOptionsContext(d.life.Context(), source, d.pluginDir, d.pluginInstallOptions())
	}
	if err != nil {
		d.sendPluginActionResult(client, action, "", false, err.Error())
		return
	}
	d.finishPluginSwap(client, action, manifest, swap)
}

func (d *Daemon) handleUpdatePluginWS(client *wsClient, msg *protocol.UpdatePluginMessage) {
	d.pluginActionMu.Lock()
	defer d.pluginActionMu.Unlock()
	name := strings.TrimSpace(msg.Name)
	if name == "" {
		d.sendPluginActionResult(client, "update", "", false, "plugin name is required")
		return
	}
	manifest, swap, err := plugins.UpdateWithOptionsContext(d.life.Context(), name, d.pluginDir, d.pluginInstallOptions())
	if err != nil {
		d.sendPluginActionResult(client, "update", name, false, err.Error())
		return
	}
	d.finishPluginSwap(client, "update", manifest, swap)
}

func (d *Daemon) handleReloadPluginWS(client *wsClient, msg *protocol.ReloadPluginMessage) {
	d.pluginActionMu.Lock()
	defer d.pluginActionMu.Unlock()
	name := strings.TrimSpace(msg.Name)
	if name == "" {
		d.sendPluginActionResult(client, "reload", "", false, "plugin name is required")
		return
	}
	manifest, err := plugins.PrepareReload(name, d.pluginDir, plugins.ReloadOptions{Env: d.pluginCommandEnv(), SkipInstall: protocol.Deref(msg.SkipInstall)})
	if err != nil {
		d.sendPluginActionResult(client, "reload", name, false, err.Error())
		return
	}
	err = d.restartInstalledPlugin(manifest)
	d.publishFact(FactPluginInstalled, name, nil)
	if err != nil {
		d.sendPluginActionResult(client, "reload", name, false, err.Error())
		return
	}
	d.sendPluginActionResult(client, "reload", name, true, "")
}

func (d *Daemon) pluginInstallOptions() plugins.InstallOptions {
	return plugins.InstallOptions{
		Env: d.pluginCommandEnv(),
		CloneGit: func(ctx context.Context, source, target string, environment []string) ([]byte, error) {
			var output []byte
			err := d.gitExecution().Run(ctx, gitTask{Kind: gitTaskPluginInstall, Lane: gitInteractive}, func(runCtx context.Context, client *attngit.Client) error {
				var runErr error
				output, runErr = client.CloneDepthOne(runCtx, source, target, environment)
				return runErr
			})
			return output, err
		},
	}
}

func (d *Daemon) finishPluginSwap(client *wsClient, action string, manifest plugins.Manifest, swap *plugins.SwapHandle) {
	defer d.publishFact(FactPluginInstalled, manifest.Name, nil)
	if !swap.Replaced() {
		if err := d.startInstalledPlugin(manifest); err != nil {
			d.logf("plugin %s installed but failed to start: %v", manifest.Name, err)
		}
		d.sendPluginActionResult(client, action, manifest.Name, true, "")
		return
	}
	if err := d.restartInstalledPlugin(manifest); err != nil {
		d.sendPluginActionResult(client, action, manifest.Name, false, d.restorePreviousPlugin(manifest.Name, swap, err))
		return
	}
	if err := swap.Commit(); err != nil {
		d.sendPluginActionResult(client, action, manifest.Name, false, d.restorePreviousPlugin(manifest.Name, swap, err))
		return
	}
	if err := swap.DiscardPrevious(); err != nil {
		d.logf("plugin %s replaced but its previous copy stayed behind: %v", manifest.Name, err)
	}
	d.sendPluginActionResult(client, action, manifest.Name, true, "")
}

func (d *Daemon) restorePreviousPlugin(name string, swap *plugins.SwapHandle, cause error) string {
	if err := swap.Rollback(); err != nil {
		return fmt.Sprintf("%v; restoring the previous copy failed: %v", cause, err)
	}
	previous, err := plugins.PrepareReload(name, d.pluginDir, plugins.ReloadOptions{SkipInstall: true})
	if err == nil {
		err = d.restartInstalledPlugin(previous)
	}
	if err != nil {
		return fmt.Sprintf("%v; restored the previous copy, which did not start again: %v", cause, err)
	}
	return fmt.Sprintf("%v; restored the previous copy", cause)
}

func (d *Daemon) handleRemovePluginWS(client *wsClient, msg *protocol.RemovePluginMessage) {
	d.uninstallPlugin(client, strings.TrimSpace(msg.Name), "remove")
}

func (d *Daemon) handleUninstallPluginWS(client *wsClient, msg *protocol.UninstallPluginMessage) {
	d.uninstallPlugin(client, strings.TrimSpace(msg.Name), "uninstall")
}

func (d *Daemon) uninstallPlugin(client *wsClient, name, action string) {
	d.pluginActionMu.Lock()
	defer d.pluginActionMu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" {
		d.sendPluginActionResult(client, action, "", false, "plugin name is required")
		return
	}
	if sessions := d.appServer.PluginSessions(name); sessions > 0 {
		d.sendPluginActionResult(client, action, name, false, fmt.Sprintf("plugin %q owns %d open server session(s)", name, sessions))
		return
	}
	if runs := d.store.ListAgentDriverRuns(name); len(runs) > 0 {
		d.sendPluginActionResult(client, action, name, false, fmt.Sprintf("plugin %q owns %d active delegated run(s)", name, len(runs)))
		return
	}
	remove := d.removePlugin
	if remove == nil {
		remove = plugins.Remove
	}
	if err := remove(d.pluginDir, name); err != nil {
		d.sendPluginActionResult(client, action, name, false, err.Error())
		return
	}
	if err := d.store.DeletePluginServerOwners(name); err != nil {
		d.sendPluginActionResult(client, action, name, false, err.Error())
		return
	}
	d.stopAndUnregisterPlugin(name)
	d.appServer.ForgetPlugin(name)

	d.publishFact(FactPluginUninstalled, name, nil)
	d.sendPluginActionResult(client, action, name, true, "")
}

func (d *Daemon) stopAndUnregisterPlugin(name string) {
	d.stopInstalledPlugin(name)
	d.disconnectPlugin(name, fmt.Errorf("plugin %q was uninstalled", name))
}

func (d *Daemon) disconnectPlugin(name string, reason error) {
	registry := d.ensurePluginRegistry()
	if connection := registry.get(name); connection != nil {
		registry.unregister(connection)
		connection.closePending(reason)
		if connection.conn != nil {
			_ = connection.conn.Close()
		}
	}
}

func (d *Daemon) handleSetPluginPriorityWS(client *wsClient, msg *protocol.SetPluginPriorityMessage) {
	name := strings.TrimSpace(msg.Name)
	if name == "" {
		d.sendPluginActionResult(client, "set_priority", "", false, "plugin name is required")
		return
	}
	if err := d.setPluginPriority(name, int(msg.Priority)); err != nil {
		d.sendPluginActionResult(client, "set_priority", name, false, err.Error())
		return
	}

	d.publishFact(FactPluginPriorityChanged, name, nil)
	d.sendPluginActionResult(client, "set_priority", name, true, "")
}

func (d *Daemon) projectPluginsUpdated() {
	d.projectSnapshot(snapshotPlugins, func() {
		d.wsHub.BroadcastValue(d.pluginsUpdatedMessage())
	})
}

func (d *Daemon) pluginsUpdatedMessage() *protocol.PluginsUpdatedMessage {
	catalog, manifestIssues := discoverPluginManifests(d.pluginDir)
	priorities := d.pluginPriorities()
	pluginInfos := make([]protocol.PluginInfo, 0, len(catalog))
	for _, manifest := range catalog {
		connection := d.ensurePluginRegistry().get(manifest.Name)
		runtime, supervised := d.ensurePluginSupervisor().Snapshot(manifest.Name)
		healthStatus := "unknown"
		var healthMessage string
		var lastHealthAt string
		if connection != nil {
			status, message, checkedAt := connection.healthSnapshot()
			healthStatus = status
			healthMessage = message
			if !checkedAt.IsZero() {
				lastHealthAt = checkedAt.Format(time.RFC3339Nano)
			}
		}
		info := protocol.PluginInfo{
			Name:         manifest.Name,
			Version:      manifest.Version,
			Dir:          manifest.Dir,
			Priority:     priorities[manifest.Name],
			Connected:    connection != nil,
			Running:      runtime.Running,
			HealthStatus: protocol.Ptr(healthStatus),
			CanUninstall: len(d.store.ListAgentDriverRuns(manifest.Name)) == 0 && d.appServer.PluginSessions(manifest.Name) == 0,
		}
		runtimePhase := pluginPhaseStopped
		if supervised {
			runtimePhase = runtime.Phase
		} else if connection != nil {
			runtimePhase = pluginPhaseConnected
		}
		info.RuntimePhase = protocol.Ptr(string(runtimePhase))
		switch {
		case runtimePhase == pluginPhaseParked:
			info.RuntimeState = pluginRuntimeStateParked
		case healthStatus == "unhealthy" || runtimePhase == pluginPhaseBackoff:
			info.RuntimeState = pluginRuntimeStateDegraded
		case connection != nil:
			info.RuntimeState = pluginRuntimeStateConnected
		case runtime.Running:
			info.RuntimeState = pluginRuntimeStateStarting
		default:
			info.RuntimeState = pluginRuntimeStateDegraded
		}
		if runtime.RestartAttempt > 0 {
			info.RestartAttempt = protocol.Ptr(runtime.RestartAttempt)
		}
		if !runtime.NextRestartAt.IsZero() {
			info.NextRestartAt = protocol.Ptr(runtime.NextRestartAt.Format(time.RFC3339Nano))
		}
		if runtime.LastExit != nil {
			info.LastExit = protocol.Ptr(runtime.LastExit.String())
		}
		if manifest.Description != "" {
			info.Description = protocol.Ptr(manifest.Description)
		}
		if manifest.LinkTarget != "" {
			info.LinkTarget = protocol.Ptr(manifest.LinkTarget)
		}
		if manifest.Source != "" {
			info.Source = protocol.Ptr(manifest.Source)
			info.SourceKind = protocol.Ptr(string(manifest.SourceKind))
		}
		if healthMessage != "" {
			info.HealthMessage = protocol.Ptr(healthMessage)
		}
		if lastHealthAt != "" {
			info.LastHealthAt = protocol.Ptr(lastHealthAt)
		}
		pluginInfos = append(pluginInfos, info)
	}
	sort.Slice(pluginInfos, func(i, j int) bool {
		if pluginInfos[i].Priority != pluginInfos[j].Priority {
			return pluginInfos[i].Priority > pluginInfos[j].Priority
		}
		return pluginInfos[i].Name < pluginInfos[j].Name
	})

	var issues []protocol.PluginIssue
	for _, issue := range manifestIssues {
		issues = append(issues, protocol.PluginIssue{
			Path:  issue.Path,
			Error: issue.Err.Error(),
		})
	}

	message := &protocol.PluginsUpdatedMessage{
		Event:   protocol.EventPluginsUpdated,
		Plugins: pluginInfos,
	}
	if len(issues) > 0 {
		message.Issues = issues
	}
	return message
}

func (d *Daemon) sendPluginActionResult(client *wsClient, action, name string, success bool, errMsg string) {
	result := &protocol.PluginActionResultMessage{
		Event:   protocol.EventPluginActionResult,
		Action:  action,
		Success: success,
	}
	if name != "" {
		result.Name = protocol.Ptr(name)
	}
	if errMsg != "" {
		result.Error = protocol.Ptr(errMsg)
	}
	d.sendToClient(client, result)
}

func (d *Daemon) pluginPriorities() map[string]int {
	raw := strings.TrimSpace(d.setting(setting.PluginPriorities))
	if raw == "" {
		return map[string]int{}
	}
	priorities := map[string]int{}
	if err := json.Unmarshal([]byte(raw), &priorities); err != nil {
		d.logf("failed to decode plugin priorities: %v", err)
		return map[string]int{}
	}
	return priorities
}

func (d *Daemon) setPluginPriority(name string, priority int) error {
	if priority < -1_000_000 || priority > 1_000_000 {
		return fmt.Errorf("plugin priority must be between -1000000 and 1000000")
	}
	priorities := d.pluginPriorities()
	priorities[name] = priority
	encoded, err := json.Marshal(priorities)
	if err != nil {
		return fmt.Errorf("encode plugin priorities: %w", err)
	}
	d.store.SetSetting(setting.PluginPriorities.Key, string(encoded))
	d.ensurePluginRegistry().setPriorities(priorities)
	return nil
}
