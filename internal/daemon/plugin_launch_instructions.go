package daemon

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	pluginInstructionKindAgent = "agent"
	pluginInstructionKindChief = "chief"
)

type pluginLaunchInstructions struct {
	Kind         string `json:"kind"`
	Content      string `json:"content"`
	ProfileID    string `json:"profile_id,omitempty"`
	NotebookRoot string `json:"notebook_root,omitempty"`
}

func (d *Daemon) preparePluginLaunchInstructions(sessionID protocol.SessionID, profileID string, isChief, selfReportPullRequests bool) (*pluginLaunchInstructions, error) {
	launch, _, err := d.prepareLaunchContext(sessionID, isChief, selfReportPullRequests)
	if err != nil {
		return nil, err
	}
	kind := pluginInstructionKindAgent
	if isChief {
		kind = pluginInstructionKindChief
	}
	return &pluginLaunchInstructions{
		Kind: kind, Content: launch.Instructions(), ProfileID: profileID, NotebookRoot: launch.NotebookRoot,
	}, nil
}

func (d *Daemon) prepareLaunchContext(sessionID protocol.SessionID, isChief, selfReportPullRequests bool) (hooks.Launch, []string, error) {
	vents := d.launchVentIntent(sessionID)
	launch := hooks.Launch{Garden: true, Asks: d.launchedWithAsks(sessionID), Vent: vents.Vent, ToolVents: vents.ToolVents, SelfReportPullRequests: selfReportPullRequests, QuickCapture: parseBooleanSetting(d.homeSetting(SettingQuickCaptureEnabled))}
	if isChief {
		root, _, err := d.ensureNotebookScaffold()
		if err != nil {
			return hooks.Launch{}, nil, fmt.Errorf("prepare chief notebook: %w", err)
		}
		if strings.TrimSpace(root) == "" {
			return hooks.Launch{}, nil, fmt.Errorf("prepare chief guidance: notebook root is empty")
		}
		launch.NotebookRoot = root
	}
	member, block, _, err := d.crewPrimeForSession(sessionID)
	if err != nil {
		d.logf("crew: refusing launch priming for session %s: %v", sessionID, err)
		if d.crewMemoryEnabled() {
			return launch, nil, err
		}
		return launch, nil, nil
	}
	launch.Crew = block
	launch.CrewMemoryEnabled = block != "" && d.crewMemoryEnabled()
	return launch, member.AwarenessDirs, nil
}
