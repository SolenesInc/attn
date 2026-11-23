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

func facts() []harness.Facts {
	return []harness.Facts{claude.Facts(), codex.Facts(), sigma.Facts()}
}

func FactsFor(id harness.ID) (harness.Facts, bool) {
	id = strings.TrimSpace(strings.ToLower(id))
	for _, f := range facts() {
		if f.ID == id {
			return f, true
		}
	}
	return harness.Facts{}, false
}

func Has(id harness.ID) bool {
	_, ok := FactsFor(id)
	return ok
}

func IDs() []harness.ID {
	all := facts()
	ids := make([]harness.ID, len(all))
	for i, f := range all {
		ids[i] = f.ID
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
	for _, f := range facts() {
		if f.Caller != (harness.CallerEnv{}) {
			callers = append(callers, f.Caller)
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
