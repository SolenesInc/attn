package daemon

import (
	"fmt"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness/appserver"
)

func (d *Daemon) restorePluginServerOwners() error {
	owners, err := d.store.PluginServerOwners()
	if err != nil {
		return err
	}
	for agent, owner := range owners {
		if reservedPluginAgent(agent) {
			d.logf("skip plugin server ownership: plugin %q claims reserved agent %q", owner, agent)
			continue
		}
		d.appServer.Restore(d.pluginServerBinding(agent, owner, nil))
	}
	return nil
}

func (d *Daemon) registerPluginDriver(plugin *pluginConnection, params pluginDriverRegisterParams) error {
	agent := normalizePluginAgent(params.Agent)
	if params.Server != nil {
		if !params.Capabilities["server_binding"] {
			return fmt.Errorf("server declaration requires server_binding capability")
		}
		if params.Capabilities["message_delivery"] || params.Capabilities["compaction"] || params.Capabilities["model_discovery"] || params.Capabilities["state_reporting"] {
			return fmt.Errorf("server_binding supplies delivery, models and state; separate handlers are not supported")
		}
		if err := params.Server.Validate(); err != nil {
			return err
		}
		for _, ability := range params.Server.UnknownAbilities() {
			d.logf("plugin %s declares server ability %q for %s, which attn does not know; ignoring it", plugin.name, ability, agent)
		}
	} else if params.Capabilities["server_binding"] {
		return fmt.Errorf("server_binding requires a server declaration")
	}
	if reservedPluginAgent(agent) {
		return fmt.Errorf("agent %q is invalid or reserved by attn", agent)
	}
	if _, err := validatePluginDriverCapabilities(params.Capabilities); err != nil {
		return err
	}
	for _, dir := range params.SkillDirs {
		if err := dir.validate(); err != nil {
			return err
		}
	}
	registry := d.ensurePluginRegistry()
	if registry.get(plugin.name) != plugin {
		return fmt.Errorf("plugin %q is not connected", plugin.name)
	}
	if len(params.SkillDirs) > 0 {
		d.ensureEnabledWorkflowSkillsIn(d.pluginSkillsDirs(params.SkillDirs))
	}
	if existing, exists := registry.driver(agent); exists && existing.PluginName != plugin.name {
		return fmt.Errorf("agent %q is already registered by plugin %q", agent, existing.PluginName)
	}
	claim := func() error {
		for _, run := range d.store.ListActiveAgentDriverRuns() {
			if session := d.store.Get(run.SessionID); session != nil && session.Agent == agent && run.PluginName != plugin.name {
				return fmt.Errorf("agent %q has a run owned by plugin %q", agent, run.PluginName)
			}
		}
		return d.store.ClaimPluginServer(agent, plugin.name)
	}
	if err := d.appServer.Bind(d.pluginServerBinding(agent, plugin.name, params.Server), claim); err != nil {
		return err
	}
	return registry.registerDriver(plugin, params)
}

func (d *Daemon) pluginServerBinding(agent, plugin string, declaration *appserver.Declaration) appserver.Binding {
	return appserver.Binding{
		Agent: agent, Plugin: plugin, Declaration: declaration,
		DisplayName: func() string { return d.factsOrNone(agent).DisplayName },
		Enabled: func() bool {
			reg, conn := d.ensurePluginRegistry().driverConnection(agent, plugin)
			return conn != nil && reg.Capabilities["server_binding"]
		},
	}
}

func reservedPluginAgent(agent string) bool {
	kind := agent + "-server"
	return agent == "" || agent == "shell" || agentdriver.Get(agent) != nil || kind == codexLinkKind || strings.HasPrefix(kind, codexServerTerminalPrefix)
}
