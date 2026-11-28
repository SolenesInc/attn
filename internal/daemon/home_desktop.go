package daemon

import (
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) listHomeDesktops() {
	d.store.ListHomeDesktop(d.on(setting.ViewsEnabled))
}

func (d *Daemon) homeDesktopsListingChanged() {
	d.listHomeDesktops()
	d.settleHiddenHomeDesktops()
	live, err := d.store.ListProfiles(false)
	if err != nil {
		d.logf("home desktop: list profiles: %v", err)
		return
	}
	for _, profile := range live {
		d.publishArrangementChanged(profile.ID)
	}
}

// settleHiddenHomeDesktops leaves, while views_enabled is off, no profile on its Home desktop and nothing on
// Home but its views, so no terminal waits where the user cannot see it.
func (d *Daemon) settleHiddenHomeDesktops() {
	if d.on(setting.ViewsEnabled) {
		return
	}
	live, err := d.store.ListProfiles(false)
	if err != nil {
		d.logf("home desktop: list profiles: %v", err)
		return
	}
	for _, profile := range live {
		if err := d.leaveHiddenHome(profile); err != nil {
			d.logf("home desktop: leave Home of profile %s: %v", profile.ID, err)
		}
	}
}

func (d *Daemon) leaveHiddenHome(profile profiles.Profile) error {
	home, err := d.store.HomeDesktop(profile.ID)
	if err != nil {
		return err
	}
	work := homeWork(home.Tree)
	if profile.CurrentDesktopID != home.ID && len(work) == 0 {
		return nil
	}
	target := profile.CurrentDesktopID
	if target == home.ID {
		if target, err = d.firstOrdinaryDesktop(profile.ID); err != nil {
			return err
		}
	}
	for _, leaf := range work {
		if _, err := d.store.MoveLeaf(store.LeafMoveRequest{SourceDesktopID: home.ID, TargetDesktopID: target, LeafID: leaf, Direction: layouttree.DirectionVertical}); err != nil {
			return err
		}
	}
	return nil
}

func (d *Daemon) firstOrdinaryDesktop(profileID string) (string, error) {
	_, desktops, err := d.store.ProfileArrangement(profileID)
	if err != nil {
		return "", err
	}
	if len(desktops) == 0 {
		_, created, err := d.store.CreateDesktop(profileID, "", 0, true)
		return created.ID, err
	}
	_, err = d.store.SetCurrentDesktop(profileID, desktops[0].ID)
	return desktops[0].ID, err
}

func homeWork(tree layouttree.Node) []string {
	work := layouttree.PaneIDs(tree)
	for _, tile := range layouttree.TileLeaves(tree) {
		if !profiles.ViewTile(tile) {
			work = append(work, tile.TileID)
		}
	}
	return work
}

func (d *Daemon) handleDesktopResetHome(client *wsClient, msg *protocol.DesktopResetHomeMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		desktops, err := d.store.ResetHomeDesktop(msg.ProfileID)
		return profileActionOutcome{desktops: desktops, arranges: true, publish: func() {
			d.publishArrangementChanged(msg.ProfileID)
		}}, err
	})
}
