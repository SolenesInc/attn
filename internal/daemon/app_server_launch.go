package daemon

import (
	"github.com/victorarias/attn/internal/harness/appserver"
	"github.com/victorarias/attn/internal/harness/builtin"
)

func (d *Daemon) adapt() {
	d.appServer = appserver.New(d.host(appServerTerminalOwner))
	d.builtins = builtin.All(d.host(appServerTerminalOwner), d.appServer)
	d.pluginHarnesses = builtin.Plugins(d.host(appServerTerminalOwner), d.appServer, pluginHost{d})
}
