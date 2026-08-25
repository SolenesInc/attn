package hostsession

import (
	"path/filepath"

	"github.com/victorarias/attn/internal/procreap"
)

func RegistryDir(dataDir string) string {
	return filepath.Join(dataDir, "hosts", "registry")
}

func RegistryPath(dataDir, sessionID string) string {
	return filepath.Join(RegistryDir(dataDir), sessionID+".json")
}

// SIGTERM first — the host answers by running pi's dispose, the only path that reaches
// its tool subprocesses — then a SIGKILL of the group after terminationGrace.
func ReapDataDir(dataDir string) []procreap.ReapResult {
	return procreap.ReapDir(RegistryDir(dataDir), terminationGrace)
}
