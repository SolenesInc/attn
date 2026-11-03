package fakeagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	PlugCapabilitiesEnv = "ATTN_FAKE_PLUG_CAPABILITIES"
	PlugAgentEnv        = "ATTN_FAKE_PLUG_AGENT"
	PlugModelsEnv       = "ATTN_FAKE_PLUG_MODELS"
	plugRefusalFile     = "refuse-launch"
)

func plugAgentName() string {
	if agent := strings.TrimSpace(os.Getenv(PlugAgentEnv)); agent != "" {
		return agent
	}
	return "plug"
}

func withPlugCapabilityOverrides(capabilities map[string]bool) map[string]bool {
	for _, field := range strings.Split(os.Getenv(PlugCapabilitiesEnv), ",") {
		name, value, _ := strings.Cut(strings.TrimSpace(field), "=")
		if name != "" {
			capabilities[name] = value != "false"
		}
	}
	return capabilities
}

type plugLaunchChoices struct {
	Model        string `json:"model"`
	Effort       string `json:"effort"`
	Instructions *struct {
		Content string `json:"content"`
	} `json:"instructions"`
}

func plugLaunchFlags(raw json.RawMessage) []string {
	var choices plugLaunchChoices
	if json.Unmarshal(raw, &choices) != nil {
		return nil
	}
	var flags []string
	if choices.Model != "" {
		flags = append(flags, "--model", choices.Model)
	}
	if choices.Effort != "" {
		flags = append(flags, "--effort", choices.Effort)
	}
	if choices.Instructions != nil && choices.Instructions.Content != "" {
		flags = append(flags, "--append-system-prompt", choices.Instructions.Content)
	}
	return flags
}

func plugLaunchRefusal() string {
	reason, err := os.ReadFile(filepath.Join(programDir(), plugRefusalFile))
	if err != nil {
		return ""
	}
	return string(reason)
}

func (k *Kit) RefusePlugLaunches(reason string) (allow func()) {
	k.t.Helper()
	path := filepath.Join(filepath.Dir(k.cfg.Bin), "plugins", plugPluginName, plugRefusalFile)
	if err := os.WriteFile(path, []byte(reason), 0o644); err != nil {
		k.t.Fatal(err)
	}
	return func() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			k.t.Fatal(err)
		}
	}
}
