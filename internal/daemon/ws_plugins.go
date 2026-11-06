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

	options := plugins.InstallOptions{
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
	var manifest plugins.Manifest
	var err error
	if link {
		manifest, err = plugins.LinkPath(source, d.pluginDir, options)
	} else {
		manifest, err = plugins.InstallSourceWithOptions(source, d.pluginDir, options)
	}
	if err != nil {
		d.sendPluginActionResult(client, action, "", false, err.Error())
		return
	}
	if err := d.startInstalledPlugin(manifest); err != nil {
		d.logf("plugin %s installed but failed to start: %v", manifest.Name, err)
	}

	d.publishFact(FactPluginInstalled, manifest.Name, nil)
	d.sendPluginActionResult(client, action, manifest.Name, true, "")
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
	d.stopAndUnregisterPlugin(name)

	d.publishFact(FactPluginUninstalled, name, nil)
	d.sendPluginActionResult(client, action, name, true, "")
}

func (d *Daemon) stopAndUnregisterPlugin(name string) {
	d.stopInstalledPlugin(name)
	registry := d.ensurePluginRegistry()
	if connection := registry.get(name); connection != nil {
		registry.unregister(connection)
		connection.closePending(fmt.Errorf("plugin %q was uninstalled", name))
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
			CanUninstall: len(d.store.ListAgentDriverRuns(manifest.Name)) == 0,
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
