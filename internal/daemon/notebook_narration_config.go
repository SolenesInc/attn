package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

const (
	notebookSummarizeSessionKind = "summarize_session"
	notebookNarrateWorkspaceKind = "narrate_workspace"
)

// Claude is the default agent for both tiers because its native Write/Edit enforce the
// read-before-write CAS the shared journal depends on (codex apply-patch CAS unverified).
const (
	notebookSummarizeDefaultAgent = "claude"
	notebookSummarizeDefaultModel = "claude-haiku-4-5"
	notebookNarrateDefaultAgent   = "claude"
	notebookNarrateDefaultModel   = "claude-sonnet-4-6"
)

type notebookNarrationConfig struct {
	Agent string `json:"agent"`
	Model string `json:"model"`
}

func narrationTierDefault(kind string) (agent, model string) {
	switch kind {
	case notebookSummarizeSessionKind:
		return notebookSummarizeDefaultAgent, notebookSummarizeDefaultModel
	default:
		return notebookNarrateDefaultAgent, notebookNarrateDefaultModel
	}
}

// Unlike parseKeeperCompactConfig, a BLANK value yields the tier DEFAULT, not a
// disabled config.
func parseNotebookNarrationConfig(kind, raw string) (notebookNarrationConfig, error) {
	raw = strings.TrimSpace(raw)
	var config notebookNarrationConfig
	if raw == "" {
		config.Agent, config.Model = narrationTierDefault(kind)
	} else {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&config); err != nil {
			return notebookNarrationConfig{}, fmt.Errorf("invalid %s configuration: %w", kind, err)
		}
		if err := ensureJSONEOF(decoder); err != nil {
			return notebookNarrationConfig{}, fmt.Errorf("invalid %s configuration: %w", kind, err)
		}
		config.Agent = strings.TrimSpace(strings.ToLower(config.Agent))
		config.Model = strings.TrimSpace(config.Model)
		if config.Agent == "" || config.Model == "" {
			return notebookNarrationConfig{}, fmt.Errorf("%s requires both agent and model", kind)
		}
	}

	driver := agentdriver.Get(config.Agent)
	if driver == nil {
		return notebookNarrationConfig{}, fmt.Errorf("%s agent is not installed: %s", kind, config.Agent)
	}
	if _, ok := driver.(agentdriver.HeadlessTaskProvider); !ok {
		return notebookNarrationConfig{}, fmt.Errorf("agent %s does not support headless tasks", config.Agent)
	}
	if available, reason := agentdriver.HeadlessTaskAvailability(driver); !available {
		return notebookNarrationConfig{}, fmt.Errorf("agent %s cannot run headless tasks: %s", config.Agent, reason)
	}
	if !agentdriver.HeadlessTasksSupportTools(driver) {
		return notebookNarrationConfig{}, fmt.Errorf("agent %s supports only tool-free headless tasks", config.Agent)
	}
	return config, nil
}

func (d *Daemon) validateNotebookNarrationSetting(kind, raw string) error {
	config, err := parseNotebookNarrationConfig(kind, raw)
	if err != nil {
		return err
	}
	driver := agentdriver.Get(config.Agent)
	configured := ""
	if d.store != nil {
		configured = d.store.GetSetting(canonicalExecutableSettingKey(config.Agent))
	}
	executable := driver.ResolveExecutable(configured)
	if _, err := exec.LookPath(executable); err != nil {
		return fmt.Errorf("%s executable for %s was not found: %w", kind, config.Agent, err)
	}
	return nil
}

func (d *Daemon) notebookNarrationConfigFor(kind string) (notebookNarrationConfig, error) {
	if d.store == nil {
		return notebookNarrationConfig{}, errors.New("notebook narration settings unavailable")
	}
	settingKey := ""
	switch kind {
	case notebookSummarizeSessionKind:
		settingKey = SettingNotebookSummarizeSession
	case notebookNarrateWorkspaceKind:
		settingKey = SettingNotebookNarrateWorkspace
	default:
		return notebookNarrationConfig{}, fmt.Errorf("unknown narration kind: %s", kind)
	}
	return parseNotebookNarrationConfig(kind, d.store.GetSetting(settingKey))
}

func (d *Daemon) resolveNotebookNarrationExecutable(
	config notebookNarrationConfig,
) (agentdriver.HeadlessTaskProvider, string, error) {
	driver := agentdriver.Get(config.Agent)
	if driver == nil {
		return nil, "", fmt.Errorf("narration agent not found: %s", config.Agent)
	}
	provider, ok := driver.(agentdriver.HeadlessTaskProvider)
	if !ok {
		return nil, "", fmt.Errorf("agent %s does not support headless tasks", config.Agent)
	}
	configured := d.store.GetSetting(canonicalExecutableSettingKey(config.Agent))
	resolvedExecutable := driver.ResolveExecutable(configured)
	executablePath, err := exec.LookPath(resolvedExecutable)
	if err != nil {
		return nil, "", fmt.Errorf("resolve %s executable: %w", config.Agent, err)
	}
	return provider, executablePath, nil
}
