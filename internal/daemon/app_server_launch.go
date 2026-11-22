package daemon

import (
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/appserver"
)

func serverTerminalOf(l *appserver.Link) func(string) harness.TerminalID {
	if l == nil {
		return nil
	}
	return l.ServerTerminal
}

func (d *Daemon) launchOnAppServer(req harness.LaunchRequest, agent harness.ID) (harness.Command, error) {
	opts := d.launchSpawnOptions(req, agent)
	l := d.appServer.Link(string(agent))
	if err := l.Launch(req); err != nil {
		return harness.Command{}, err
	}
	onServer := l != nil && req.Lifetime == harness.InServer
	var shared *agentdriver.SharedAgentTools
	if onServer {
		if tools := l.SharedTools(); tools != nil {
			shared = &agentdriver.SharedAgentTools{Name: tools.Name, Description: tools.Description, Instructions: tools.Instructions}
		}
	}
	command, err := d.launchWithDriver(req, &opts, onServer, shared)
	if err == nil && onServer {
		command.Env = append(command.Env, l.HomeEnv())
	}
	return command, err
}

func (d *Daemon) bindBuiltInServers() {
	for _, name := range agentdriver.List() {
		declaration, ok := agentdriver.DeclaredServer(agentdriver.Get(name))
		if !ok {
			continue
		}
		binding := appserver.Binding{Agent: name, Declaration: &declaration, DisplayName: func() string { return d.factsOrNone(name).DisplayName }, Enabled: func() bool { return d.offered(name) }}
		if err := d.appServer.Bind(binding, nil); err != nil {
			d.logf("link the %s server: %v", name, err)
		}
	}
}
