package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
)

type pluginServerDeclaration struct {
	Protocol      string   `json:"protocol"`
	Executable    string   `json:"executable"`
	ExecutableEnv string   `json:"executable_env,omitempty"`
	ServeArgs     []string `json:"serve_args"`
	HomeEnv       string   `json:"home_env"`
	DefaultHome   string   `json:"default_home"`
	SocketName    string   `json:"socket_name"`
	Initialize    struct {
		Server   string `json:"server,omitempty"`
		Protocol struct {
			Exact int `json:"exact,omitempty"`
			Min   int `json:"min,omitempty"`
		} `json:"protocol"`
	} `json:"initialize"`
	CallerConversationEnv string `json:"caller_conversation_env"`
	OnClose               string `json:"on_close"`
	Recovery              string `json:"recovery"`
	Targets               bool   `json:"targets,omitempty"`
}

func validEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		if c != '_' && !(c >= 'A' && c <= 'Z') && !(c >= 'a' && c <= 'z') && !(i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func (p *pluginServerDeclaration) validate() error {
	if p.Protocol != "attn-app-server-v1" {
		return fmt.Errorf("unsupported server binding protocol %q; need attn-app-server-v1", p.Protocol)
	}
	if strings.TrimSpace(p.Executable) == "" {
		return fmt.Errorf("server executable is required")
	}
	if !validEnvironmentName(p.HomeEnv) || !validEnvironmentName(p.CallerConversationEnv) || (p.ExecutableEnv != "" && !validEnvironmentName(p.ExecutableEnv)) {
		return fmt.Errorf("server environment names must be valid variable names")
	}
	if !strings.HasSuffix(p.CallerConversationEnv, "_ROOT_CONVERSATION") || len(p.CallerConversationEnv) == len("_ROOT_CONVERSATION") {
		return fmt.Errorf("server caller_conversation_env must be named <NAME>_ROOT_CONVERSATION")
	}
	home := filepath.Clean(p.DefaultHome)
	if p.DefaultHome == "" || filepath.IsAbs(home) || home == ".." || strings.HasPrefix(home, ".."+string(filepath.Separator)) {
		return fmt.Errorf("server default_home must be relative to the user's home")
	}
	if p.SocketName == "" || filepath.Base(p.SocketName) != p.SocketName || p.SocketName == "." || p.SocketName == ".." {
		return fmt.Errorf("server socket_name must be a filename")
	}
	v := p.Initialize.Protocol
	if (v.Exact > 0) == (v.Min > 0) || v.Exact < 0 || v.Min < 0 {
		return fmt.Errorf("server initialize.protocol requires either a positive exact or min version")
	}
	if p.OnClose != "interrupt" && p.OnClose != "leave-running" {
		return fmt.Errorf("unsupported server on_close %q", p.OnClose)
	}
	if p.Recovery != "last-run" && p.Recovery != "numbered-completions" {
		return fmt.Errorf("unsupported server recovery %q", p.Recovery)
	}
	return nil
}

func (p *pluginServerDeclaration) connect(ctx context.Context, socket, client, version string) (*effectrpc.Client, effectrpc.ServerInfo, error) {
	c, err := effectrpc.Dial(ctx, socket, p.Initialize.Server)
	if err != nil {
		return nil, effectrpc.ServerInfo{}, err
	}
	requested := p.Initialize.Protocol.Exact
	if requested == 0 {
		requested = p.Initialize.Protocol.Min
	}
	var hello effectrpc.ServerInfo
	err = c.Call(ctx, "initialize", map[string]any{"client": client, "version": version, "protocol": requested}, &hello)
	if err == nil {
		err = p.validateServerInfo(hello, socket)
	}
	if err != nil {
		c.Close()
		return nil, hello, err
	}
	return c, hello, nil
}

func pluginServerBinding(agent, owner string) *serverBinding {
	return &serverBinding{agent: agent, name: agent, kind: agent + "-server", terminal: harness.TerminalID(agent + "-server"), plugin: owner,
		conversationOf: func(t harness.TerminalID) (string, bool) {
			conversation, ok := strings.CutPrefix(string(t), "server:"+agent+":")
			return conversation, ok && conversation != ""
		},
	}
}

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
		d.serverLinks[agent] = newServerLink(d, pluginServerBinding(agent, owner))
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
		if err := params.Server.validate(); err != nil {
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
			l = newServerLink(d, pluginServerBinding(agent, plugin.name))
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

func (p *pluginServerDeclaration) validateServerInfo(hello effectrpc.ServerInfo, socket string) error {
	requested := p.Initialize.Protocol.Exact
	if requested == 0 {
		requested = p.Initialize.Protocol.Min
	}
	if (p.Initialize.Server != "" && hello.Server != p.Initialize.Server) || (p.Initialize.Protocol.Exact > 0 && hello.Protocol != requested) || hello.Protocol < requested {
		return &effectrpc.Error{Server: p.Initialize.Server, Method: "initialize", Message: fmt.Sprintf("server at %s is %q %s speaking protocol %d; binding requires identity %q and protocol %+v", socket, hello.Server, hello.Version, hello.Protocol, p.Initialize.Server, p.Initialize.Protocol)}
	}
	return nil
}
