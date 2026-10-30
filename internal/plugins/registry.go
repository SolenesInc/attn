package plugins

import (
	"github.com/victorarias/attn/internal/stepclock"
	"path/filepath"
	"time"

	"github.com/victorarias/attn/internal/procreap"
)

func RuntimeRegistryDir(dataDir string) string {
	return filepath.Join(dataDir, "plugin-runtime", "registry")
}

const runtimeTerminationGrace = 3 * time.Second

func ReapRuntimeProcesses(clock *stepclock.Clock, dataDir string) []procreap.ReapResult {
	return procreap.ReapDir(clock, RuntimeRegistryDir(dataDir), runtimeTerminationGrace)
}
