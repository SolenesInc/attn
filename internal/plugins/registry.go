package plugins

import (
	"path/filepath"
	"time"

	"github.com/victorarias/attn/internal/procreap"
)

// A daemon that dies without running its shutdown leaves plugin runtime processes reparented to init, findable only through the procreap entry the supervisor writes per spawn; `attn profile clean` reaps from it.

// Distinct from PluginDir (<dataDir>/plugins), which holds installed plugin code.
func RuntimeRegistryDir(dataDir string) string {
	return filepath.Join(dataDir, "plugin-runtime", "registry")
}

// A tripwire, not a budget: drivers exit immediately on SIGTERM (the attn-pi runtime carries no handler), so 3s is far past that.
const runtimeTerminationGrace = 3 * time.Second

// A caller about to remove the data dir must reap first — deleting the registry strands any process still alive.
func ReapRuntimeProcesses(dataDir string) []procreap.ReapResult {
	return procreap.ReapDir(RuntimeRegistryDir(dataDir), runtimeTerminationGrace)
}
