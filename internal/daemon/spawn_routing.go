package daemon

import "github.com/victorarias/attn/internal/config"

func (d *Daemon) runEnv(lifecycleID string) []string {
	return append(d.spawnRoutingEnv(), "ATTN_RUNTIME_LIFECYCLE="+lifecycleID)
}

func (d *Daemon) spawnRoutingEnv() []string {
	return append(config.SessionMarkerEnv(d.dataRoot),
		"ATTN_INSTANCE="+config.Instance(),
		"ATTN_DATA_DIR="+d.dataRoot,
		"ATTN_DB_PATH="+config.DBPath(),
		"ATTN_SOCKET_PATH="+d.socketPath,
		"ATTN_WS_PORT="+config.WSPort(),
		"ATTN_CONFIG_PATH="+config.ConfigPath(),
		"ATTN_PLUGIN_DIR="+d.pluginDir,
	)
}
