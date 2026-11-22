package daemon

import (
	"context"

	"github.com/victorarias/attn/internal/harness"
)

type pluginHost struct{ d *Daemon }

func (p pluginHost) Drivers() []harness.PluginDriver {
	return p.d.ensurePluginRegistry().registeredDrivers()
}

func (p pluginHost) Driver(agent harness.ID) (harness.PluginDriver, bool) {
	return p.d.ensurePluginRegistry().driver(agent)
}

func (p pluginHost) Health(plugin string) (status, detail string, connected bool) {
	connection := p.d.ensurePluginRegistry().get(plugin)
	if connection == nil {
		return "", "", false
	}
	status, detail, _ = connection.healthSnapshot()
	return status, detail, true
}

func (p pluginHost) Call(ctx context.Context, plugin, method string, params, result any) error {
	return p.d.callPlugin(ctx, plugin, method, params, result)
}
