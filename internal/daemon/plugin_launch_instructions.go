package daemon

import (
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

const pluginInstructionKindAgent = "agent"

type pluginLaunchInstructions struct {
	Kind      string `json:"kind"`
	Content   string `json:"content"`
	ProfileID string `json:"profile_id,omitempty"`
}

func renderPluginLaunchInstructions(launch hooks.Launch, profileID string, agentTools bool) *pluginLaunchInstructions {
	launch.AgentTools = agentTools
	return &pluginLaunchInstructions{Kind: pluginInstructionKindAgent, Content: launch.Instructions(), ProfileID: profileID}
}

func (d *Daemon) prepareLaunchContext(sessionID protocol.SessionID, selfReportPullRequests bool) (hooks.Launch, []string, error) {
	vents := d.launchVentIntent(sessionID)
	launch := hooks.Launch{Garden: true, Asks: d.launchedWithAsks(sessionID), Vent: vents.Vent, ToolVents: vents.ToolVents, CodeReview: d.on(setting.CodeReviewEnabled), SelfReportPullRequests: selfReportPullRequests}
	member, block, _, err := d.crewPrimeForSession(sessionID)
	if err != nil {
		d.logf("crew: refusing launch priming for session %s: %v", sessionID, err)
		return launch, nil, err
	}
	launch.Crew = block
	return launch, member.AwarenessDirs, nil
}
