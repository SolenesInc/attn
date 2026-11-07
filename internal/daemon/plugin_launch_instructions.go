package daemon

import (
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

const pluginInstructionKindAgent = "agent"

type pluginLaunchInstructions struct {
	Kind         string `json:"kind"`
	Content      string `json:"content"`
	ProfileID    string `json:"profile_id,omitempty"`
	NotebookRoot string `json:"notebook_root,omitempty"`
}

func (d *Daemon) preparePluginLaunchInstructions(sessionID protocol.SessionID, profileID string, selfReportPullRequests bool) (*pluginLaunchInstructions, error) {
	launch, _, err := d.prepareLaunchContext(sessionID, selfReportPullRequests)
	if err != nil {
		return nil, err
	}
	return &pluginLaunchInstructions{
		Kind: pluginInstructionKindAgent, Content: launch.Instructions(), ProfileID: profileID, NotebookRoot: launch.NotebookRoot,
	}, nil
}

func (d *Daemon) prepareLaunchContext(sessionID protocol.SessionID, selfReportPullRequests bool) (hooks.Launch, []string, error) {
	vents := d.launchVentIntent(sessionID)
	launch := hooks.Launch{Garden: true, Asks: d.launchedWithAsks(sessionID), Vent: vents.Vent, ToolVents: vents.ToolVents, SelfReportPullRequests: selfReportPullRequests, QuickCapture: d.on(setting.QuickCaptureEnabled)}
	member, block, _, err := d.crewPrimeForSession(sessionID)
	if err != nil {
		d.logf("crew: refusing launch priming for session %s: %v", sessionID, err)
		return launch, nil, err
	}
	launch.Crew = block
	return launch, member.AwarenessDirs, nil
}
