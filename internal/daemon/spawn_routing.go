package daemon

import "github.com/victorarias/attn/internal/config"

func (d *Daemon) spawnRoutingEnv() []string {
	return []string{
		"ATTN_PROFILE=" + config.Profile(),
		"ATTN_DATA_DIR=" + d.dataRoot,
		"ATTN_DB_PATH=" + config.DBPath(),
		"ATTN_SOCKET_PATH=" + d.socketPath,
		"ATTN_WS_PORT=" + config.WSPort(),
		"ATTN_CONFIG_PATH=" + config.ConfigPath(),
		"ATTN_PLUGIN_DIR=" + d.pluginDir,
	}
}
