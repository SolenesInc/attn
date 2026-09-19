package plugins

import (
	"path/filepath"
	"time"

	"github.com/victorarias/attn/internal/procreap"
)

func RuntimeRegistryDir(dataDir string) string {
	return filepath.Join(dataDir, "plugin-runtime", "registry")
}

const runtimeTerminationGrace = 3 * time.Second

func ReapRuntimeProcesses(dataDir string) []procreap.ReapResult {
	return procreap.ReapDir(RuntimeRegistryDir(dataDir), runtimeTerminationGrace)
}
