// Package builtin holds the harnesses built into attn.
package builtin

import (
	"strings"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/appserver"
	"github.com/victorarias/attn/internal/harness/internal/claude"
	"github.com/victorarias/attn/internal/harness/internal/codex"
	"github.com/victorarias/attn/internal/harness/internal/plugin"
	"github.com/victorarias/attn/internal/harness/internal/sigma"
)

// All are the built-in harnesses, their servers bound on servers.
func All(host func(owner string) harness.Host, servers *appserver.Client) []harness.Harness {
	return []harness.Harness{claude.New(host("")), codex.New(host), sigma.New(host(""), servers)}
}

func FactsFor(id harness.ID) (harness.Facts, bool) {
	if l := Launcher(id); l != nil {
		return l.Facts(), true
	}
	return harness.Facts{}, false
}

func Has(id harness.ID) bool {
	_, ok := FactsFor(id)
	return ok
}

func IDs() []harness.ID {
	all := launchers()
	ids := make([]harness.ID, len(all))
	for i, l := range all {
		ids[i] = l.Facts().ID
	}
	return ids
}

func launchers() []harness.DirectLauncher {
	return []harness.DirectLauncher{claude.Launcher(), codex.Launcher(), sigma.Launcher()}
}

func Launcher(id harness.ID) harness.DirectLauncher {
	id = strings.TrimSpace(strings.ToLower(id))
	for _, l := range launchers() {
		if l.Facts().ID == id {
			return l
		}
	}
	return nil
}

func CallerEnvs() []harness.CallerEnv {
	var callers []harness.CallerEnv
	for _, l := range launchers() {
		if caller := l.Facts().Caller; caller != (harness.CallerEnv{}) {
			callers = append(callers, caller)
		}
	}
	return callers
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
