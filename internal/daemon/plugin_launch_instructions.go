package daemon

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/hooks"
)

const (
	pluginInstructionKindAgent = "agent"
	pluginInstructionKindChief = "chief"
)

type pluginLaunchInstructions struct {
	Kind         string `json:"kind"`
	Content      string `json:"content"`
	WorkspaceID  string `json:"workspace_id,omitempty"`
	NotebookRoot string `json:"notebook_root,omitempty"`
}

func (d *Daemon) preparePluginLaunchInstructions(sessionID, workspaceID string, isChief, selfReportPullRequests bool) (*pluginLaunchInstructions, error) {
	gardenHome := d.requireHome(garden.Surface) == nil
	if isChief {
		root, _, err := d.ensureNotebookScaffold()
		if err != nil {
			return nil, fmt.Errorf("prepare chief notebook: %w", err)
		}
		if strings.TrimSpace(root) == "" {
			return nil, fmt.Errorf("prepare chief guidance: notebook root is empty")
		}
		return &pluginLaunchInstructions{
			Kind: pluginInstructionKindChief,
			Content: hooks.Launch{
				NotebookRoot:           root,
				Garden:                 gardenHome,
				Crew:                   d.crewPrimeForLaunch(sessionID),
				SelfReportPullRequests: selfReportPullRequests,
			}.Instructions(),
			WorkspaceID:  workspaceID,
			NotebookRoot: root,
		}, nil
	}

	return &pluginLaunchInstructions{
		Kind: pluginInstructionKindAgent,
		Content: hooks.Launch{
			InjectWorkflow:         parseBooleanSetting(d.store.GetSetting(SettingWorkflowsEnabled)),
			Garden:                 gardenHome,
			Crew:                   d.crewPrimeForLaunch(sessionID),
			SelfReportPullRequests: selfReportPullRequests,
		}.Instructions(),
		WorkspaceID: workspaceID,
	}, nil
}

func (d *Daemon) crewPrimeForLaunch(sessionID string) string {
	_, block, _, err := d.crewPrimeForSession(sessionID)
	if err != nil {
		d.logf("crew: refusing launch priming for session %s: %v", sessionID, err)
		return ""
	}
	return block
}
