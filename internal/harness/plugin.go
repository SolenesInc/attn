package harness

import "context"

// PluginDriver is a harness a plugin registered with attn.
type PluginDriver struct {
	Plugin      string
	Agent       ID
	DisplayName string
	Logo        Logo
	// Capabilities are the abilities the plugin declared, by name.
	Capabilities map[string]bool
	SkillDirs    func(lookupEnv func(string) string, homeDir string) ([]string, error)
}

// PluginHost is how a plugin harness reaches the plugins attn runs.
type PluginHost interface {
	Drivers() []PluginDriver
	Driver(agent ID) (PluginDriver, bool)
	// Health is the plugin's last reported health, and whether it is connected.
	Health(plugin string) (status, detail string, connected bool)
	// Call asks the plugin a question over its connection and decodes the answer into result.
	Call(ctx context.Context, plugin, method string, params, result any) error
}
