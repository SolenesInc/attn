package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

func (d *Daemon) prepareLaunchContext(sessionID protocol.SessionID, selfReportPullRequests bool) (hooks.Launch, []string, error) {
	vents := d.launchVentIntent(sessionID)
	gardenV2 := d.on(setting.GardenV2Enabled)
	launch := hooks.Launch{
		Garden:                 true,
		GardenV2:               gardenV2,
		Asks:                   d.launchedWithAsks(sessionID),
		Vent:                   vents.Vent,
		ToolVents:              vents.ToolVents,
		CodeReview:             d.on(setting.CodeReviewEnabled),
		SelfReportPullRequests: selfReportPullRequests,
	}
	if gardenV2 && d.store != nil {
		if profileID, err := d.store.SessionProfileID(sessionID); err == nil && profileID != "" {
			if beds, _, _, err := d.bedsInProfile(profileID); err == nil && len(beds) > 0 {
				names := make([]string, len(beds))
				for i, bed := range beds {
					names[i] = bed.Name
				}
				launch.GardenBeds = strings.Join(names, ", ")
			}
		}
	}
	member, block, _, err := d.crewPrimeForSession(sessionID)
	if err != nil {
		d.logf("crew: refusing launch priming for session %s: %v", sessionID, err)
		return launch, nil, err
	}
	launch.Crew = block
	return launch, member.AwarenessDirs, nil
}
