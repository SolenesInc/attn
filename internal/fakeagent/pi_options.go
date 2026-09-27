package fakeagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	PiCapabilitiesEnv = "ATTN_FAKE_PI_CAPABILITIES"
	PiAgentEnv        = "ATTN_FAKE_PI_AGENT"
	PiModelsEnv       = "ATTN_FAKE_PI_MODELS"
	piRefusalFile     = "refuse-launch"
)

func piAgentName() string {
	if agent := strings.TrimSpace(os.Getenv(PiAgentEnv)); agent != "" {
		return agent
	}
	return "pi"
}

func withPiCapabilityOverrides(capabilities map[string]bool) map[string]bool {
	for _, field := range strings.Split(os.Getenv(PiCapabilitiesEnv), ",") {
		name, value, _ := strings.Cut(strings.TrimSpace(field), "=")
		if name != "" {
			capabilities[name] = value != "false"
		}
	}
	return capabilities
}

type piLaunchChoices struct {
	Model        string `json:"model"`
	Effort       string `json:"effort"`
	Instructions *struct {
		Content string `json:"content"`
	} `json:"instructions"`
}

func piLaunchFlags(raw json.RawMessage) []string {
	var choices piLaunchChoices
	if json.Unmarshal(raw, &choices) != nil {
		return nil
	}
	var flags []string
	if choices.Model != "" {
		flags = append(flags, "--model", choices.Model)
	}
	if choices.Effort != "" {
		flags = append(flags, "--thinking", choices.Effort)
	}
	if choices.Instructions != nil && choices.Instructions.Content != "" {
		flags = append(flags, "--append-system-prompt", choices.Instructions.Content)
	}
	return flags
}

func piLaunchRefusal() string {
	reason, err := os.ReadFile(filepath.Join(programDir(), piRefusalFile))
	if err != nil {
		return ""
	}
	return string(reason)
}

func (k *Kit) RefusePiLaunches(reason string) (allow func()) {
	k.t.Helper()
	path := filepath.Join(filepath.Dir(k.cfg.Bin), "plugins", piPluginName, piRefusalFile)
	if err := os.WriteFile(path, []byte(reason), 0o644); err != nil {
		k.t.Fatal(err)
	}
	return func() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			k.t.Fatal(err)
		}
	}
}
