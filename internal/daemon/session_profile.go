package daemon

import (
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type placementOutcome struct {
	desktopID string
	paneID    string
	err       error
}

type launchPlacement struct {
	desktopID    string
	anchorPaneID string
	direction    layouttree.Direction
	focus        bool
}

func (p *launchPlacement) targetDesktop(profile profiles.Profile) string {
	if p.desktopID != "" {
		return p.desktopID
	}
	return profile.CurrentDesktopID
}

func requestedLaunchPlacement(requested *protocol.SessionPlacement) *launchPlacement {
	if requested == nil {
		return nil
	}
	return &launchPlacement{
		desktopID:    strings.TrimSpace(protocol.Deref(requested.DesktopID)),
		anchorPaneID: strings.TrimSpace(protocol.Deref(requested.AnchorPaneID)),
		direction:    layoutDirection(requested.Direction),
	}
}

func (d *Daemon) liveLaunchProfile(profileID string) (profiles.Profile, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return profiles.Profile{}, errors.New("missing profile_id: every agent belongs to a profile")
	}
	return d.store.LiveProfile(profileID)
}

func (d *Daemon) requestedOrRecentProfile(profileID string) (profiles.Profile, error) {
	if strings.TrimSpace(profileID) == "" {
		return d.store.MostRecentlyUsedProfile()
	}
	return d.liveLaunchProfile(profileID)
}

func (d *Daemon) checkLaunchPlacement(profile profiles.Profile, placement *launchPlacement) error {
	if placement == nil {
		return nil
	}
	desktop, err := d.store.LaunchDesktop(profile.ID, placement.desktopID)
	if err != nil {
		return err
	}
	if placement.anchorPaneID != "" && !layouttree.HasPane(desktop.Tree, placement.anchorPaneID) {
		return fmt.Errorf("anchor pane %q does not belong to desktop %s", placement.anchorPaneID, desktop.ID)
	}
	return nil
}

func (d *Daemon) placeLaunchedSession(session *protocol.Session, placement *launchPlacement) placementOutcome {
	if placement == nil {
		return placementOutcome{}
	}
	desktop, paneID, err := d.store.PlaceLaunchedSession(store.SessionPlacementRequest{
		DesktopID:    placement.desktopID,
		SessionID:    session.ID,
		AnchorPaneID: placement.anchorPaneID,
		Direction:    placement.direction,
		Title:        session.Label,
		Status:       profiles.PaneStatusReady,
		Focus:        placement.focus,
	})
	if err != nil {
		err = fmt.Errorf("session %s stays unplaced in profile %s: placing it on desktop %q beside pane %q failed: %w",
			session.ID, session.ProfileID, placement.desktopID, placement.anchorPaneID, err)
		d.logf("%v", err)
		return placementOutcome{err: err}
	}
	d.publishArrangementChanged(desktop.ProfileID)
	return placementOutcome{desktopID: desktop.ID, paneID: paneID}
}

func (d *Daemon) announceUnplacement(sessionID string) func() {
	placement, placed, err := d.store.SessionPlacement(sessionID)
	if err != nil || !placed {
		return func() {}
	}
	return func() {
		if _, stillPlaced, err := d.store.SessionPlacement(sessionID); err == nil && !stillPlaced {
			d.publishArrangementChanged(placement.ProfileID)
		}
	}
}

func (d *Daemon) placementBeside(sessionID string) *launchPlacement {
	placement, placed, err := d.store.SessionPlacement(sessionID)
	if err != nil {
		d.logf("reading the placement of session %s: %v", sessionID, err)
		return nil
	}
	if !placed {
		return nil
	}
	return &launchPlacement{desktopID: placement.DesktopID, anchorPaneID: placement.PaneID, direction: layouttree.DirectionVertical}
}

func (d *Daemon) callerProfile(callerSessionID string) (profiles.Profile, error) {
	callerSessionID = strings.TrimSpace(callerSessionID)
	if callerSessionID == "" {
		return d.store.MostRecentlyUsedProfile()
	}
	profileID, err := d.store.SessionProfileID(callerSessionID)
	if err != nil {
		return profiles.Profile{}, fmt.Errorf("resolve the profile of calling session %s: %w", callerSessionID, err)
	}
	return d.liveLaunchProfile(profileID)
}
