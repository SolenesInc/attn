package daemon

import (
	"fmt"
	"reflect"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

func (d *Daemon) restorePluginServerOwners() error {
	owners, err := d.store.PluginServerOwners()
	if err != nil {
		return err
	}
	d.serverLinksMu.Lock()
	defer d.serverLinksMu.Unlock()
	if d.serverLinks == nil {
		d.serverLinks = make(map[string]*serverLink)
	}
	for agent, owner := range owners {
		if reservedPluginAgent(agent) {
			d.logf("skip plugin server ownership: plugin %q claims reserved agent %q", owner, agent)
			continue
		}
		d.serverLinks[agent] = newServerLink(d, newServerBinding(agent, owner, nil))
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
	} else if params.Capabilities["server_binding"] {
		return fmt.Errorf("server_binding requires a server declaration")
	}
	if reservedPluginAgent(agent) {
		return fmt.Errorf("agent %q is invalid or reserved by attn", agent)
	}
	if _, err := validatePluginDriverCapabilities(params.Capabilities); err != nil {
		return err
	}
	registry := d.ensurePluginRegistry()
	if registry.get(plugin.name) != plugin {
		return fmt.Errorf("plugin %q is not connected", plugin.name)
	}
	d.serverLinksMu.Lock()
	defer d.serverLinksMu.Unlock()
	if existing, exists := registry.driver(agent); exists && existing.PluginName != plugin.name {
		return fmt.Errorf("agent %q is already registered by plugin %q", agent, existing.PluginName)
	}
	l := d.serverLinks[agent]
	if l != nil && l.b.plugin != plugin.name {
		return fmt.Errorf("agent %q belongs to plugin %q", agent, l.b.plugin)
	}
	if l != nil && params.Server == nil {
		return fmt.Errorf("agent %q requires its server binding", agent)
	}
	if l != nil && l.b.declaration.Load() != nil && !reflect.DeepEqual(l.b.declaration.Load(), params.Server) {
		return fmt.Errorf("server binding for %q is already registered; restart the daemon to change it", agent)
	}
	if params.Server != nil {
		for _, run := range d.store.ListActiveAgentDriverRuns() {
			if session := d.store.Get(run.SessionID); session != nil && session.Agent == agent && run.PluginName != plugin.name {
				return fmt.Errorf("agent %q has a run owned by plugin %q", agent, run.PluginName)
			}
		}
		if err := d.store.ClaimPluginServer(agent, plugin.name); err != nil {
			return err
		}
		if l == nil {
			l = newServerLink(d, newServerBinding(agent, plugin.name, nil))
			if d.serverLinks == nil {
				d.serverLinks = make(map[string]*serverLink)
			}
			d.serverLinks[agent] = l
		}
		l.b.declaration.Store(params.Server)
	}
	return registry.registerDriver(plugin, params)
}

func (d *Daemon) pluginServerSessions(plugin string) int {
	count := 0
	for _, l := range d.appServerLinks() {
		if l.b.plugin == plugin {
			count += len(l.sessions())
		}
	}
	return count
}

func (l *serverLink) hasTargets() bool {
	p := l.b.declaration.Load()
	return p != nil && p.Targets
}

func reservedPluginAgent(agent string) bool {
	kind := agent + "-server"
	return agent == "" || agent == "shell" || agentdriver.Get(agent) != nil || kind == codexLinkKind || strings.HasPrefix(kind, codexServerTerminalPrefix)
}
