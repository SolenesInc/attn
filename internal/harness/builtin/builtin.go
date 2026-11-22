// Package builtin holds the harnesses built into attn.
package builtin

import (
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/appserver"
	"github.com/victorarias/attn/internal/harness/internal/claude"
	"github.com/victorarias/attn/internal/harness/internal/plugin"
	"github.com/victorarias/attn/internal/harness/internal/sigma"
)

// All are the built-in harnesses, their servers bound on servers.
func All(host func(owner string) harness.Host, servers *appserver.Client) []harness.Harness {
	return []harness.Harness{sigma.New(host(""), servers), claude.New(host(""))}
}

// PluginHarnesses are the harnesses of the agents plugins register.
type PluginHarnesses interface {
	Harnesses() []harness.Harness
	// Unconnected is the harness of an agent no connected plugin registers.
	Unconnected(id harness.ID) harness.Harness
}

// Plugins are the harnesses of the agents plugins register through plugins, their servers bound on servers.
func Plugins(host harness.Host, servers *appserver.Client, plugins harness.PluginHost) PluginHarnesses {
	return plugin.New(host, servers, plugins)
}
