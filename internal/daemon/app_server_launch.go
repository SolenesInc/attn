package daemon

import (
	"github.com/victorarias/attn/internal/harness/appserver"
	"github.com/victorarias/attn/internal/harness/builtin"
)

func (d *Daemon) adapt() {
	host := d.host(appServerTerminalOwner)
	d.appServer = appserver.New(host)
	d.builtins = builtin.All(host, d.appServer)
	for _, source := range harnessSources {
		d.builtins = append(d.builtins, source(d, host)...)
	}
	d.pluginHarnesses = builtin.Plugins(host, d.appServer, pluginHost{d})
}
