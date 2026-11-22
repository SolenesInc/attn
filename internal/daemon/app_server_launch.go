package daemon

import (
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/appserver"
	"github.com/victorarias/attn/internal/harness/builtin"
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

func (d *Daemon) adapt() {
	d.appServer = appserver.New(d.host(appServerTerminalOwner))
	d.builtins = builtin.All(d.host(appServerTerminalOwner), d.appServer)
}
