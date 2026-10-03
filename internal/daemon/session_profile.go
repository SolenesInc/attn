package daemon

import (
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/harness"
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
	kind         string
	itemID       string
	reopen       bool
	// terminal is the one the launch runs in, recorded on the pane it gets.
	terminal harness.TerminalID
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
	if placement == nil || placement.kind != "" || placement.reopen {
		return nil
	}
	desktop, err := d.store.LaunchDesktop(profile.ID, placement.desktopID)
	var missing *profiles.Error
	if placement.desktopID != "" && errors.As(err, &missing) && missing.Code == profiles.CodeNotFound {
		*placement = launchPlacement{direction: placement.direction}
		return nil
	}
	if err != nil {
		return err
	}
	if placement.anchorPaneID != "" && !layouttree.HasLeaf(desktop.Tree, placement.anchorPaneID) {
		return fmt.Errorf("anchor leaf %q does not belong to desktop %s", placement.anchorPaneID, desktop.ID)
	}
	return nil
}

func (d *Daemon) placeAnsweringRequester(session *protocol.Session, placement *launchPlacement, requester *wsClient) placementOutcome {
	if requester == nil {
		return d.placeLaunchedSession(session, placement)
	}
	requester.holdArrangements()
	defer d.releaseArrangements(requester)
	placed := d.placeLaunchedSession(session, placement)
	if placed.paneID != "" {
		d.sendArrangement(requester, session.ID, nil)
	}
	return placed
}

func (d *Daemon) placeLaunchedSession(session *protocol.Session, placement *launchPlacement) placementOutcome {
	if placement == nil {
		return placementOutcome{}
	}
	var desktop profiles.Desktop
	var paneID string
	var err error
	if placement.kind != "" || placement.reopen {
		desktop, paneID, err = d.store.PlaceBackgroundSession(session.ID, string(placement.terminal), placement.kind, placement.itemID, placement.reopen)
	} else {
		desktop, paneID, err = d.store.PlaceLaunchedSession(store.SessionPlacementRequest{
			DesktopID:    placement.desktopID,
			SessionID:    session.ID,
			RuntimeID:    string(placement.terminal),
			AnchorPaneID: placement.anchorPaneID,
			Direction:    placement.direction,
			Title:        session.Label,
			Status:       profiles.PaneStatusReady,
			Focus:        placement.focus,
		})
	}
	if err != nil {
		err = fmt.Errorf("cannot place session %s in profile %s: placing it on desktop %q beside pane %q failed: %w",
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

// resolveDesktopRef names a desktop of profile by digit, name or id; an id on
// another profile is refused naming both profiles.
func (d *Daemon) resolveDesktopRef(profile profiles.Profile, ref string) (profiles.Desktop, error) {
	_, desktops, err := d.store.ProfileArrangement(profile.ID)
	if err != nil {
		return profiles.Desktop{}, fmt.Errorf("read the desktops of profile %q: %w", profile.Name, err)
	}
	desktop, resolveErr := profiles.ResolveDesktopRef(profile, desktops, ref)
	if resolveErr == nil {
		return desktop, nil
	}
	elsewhere, err := d.store.GetDesktop(strings.TrimSpace(ref))
	if err != nil || elsewhere.ProfileID == profile.ID {
		return profiles.Desktop{}, resolveErr
	}
	owner, err := d.store.GetProfile(elsewhere.ProfileID)
	if err != nil {
		return profiles.Desktop{}, resolveErr
	}
	return profiles.Desktop{}, profiles.Errorf(profiles.CodeCrossProfile, "desktop %s belongs to profile %q, not profile %q; %s",
		elsewhere.ID, owner.Name, profile.Name, profiles.DesktopDirectory(profile, desktops))
}

func (d *Daemon) sessionProfileID(sessionID string) (string, error) {
	profileID, err := d.store.SessionProfileID(sessionID)
	var missing *profiles.Error
	if !errors.As(err, &missing) || missing.Code != profiles.CodeNotFound || d.hubManager == nil {
		return profileID, err
	}
	if session := d.hubManager.RemoteSession(sessionID); session != nil {
		return session.ProfileID, nil
	}
	return profileID, err
}

func (d *Daemon) callerProfile(callerSessionID string) (profiles.Profile, error) {
	callerSessionID = strings.TrimSpace(callerSessionID)
	if callerSessionID == "" {
		return d.store.MostRecentlyUsedProfile()
	}
	profileID, err := d.sessionProfileID(callerSessionID)
	if err != nil {
		return profiles.Profile{}, fmt.Errorf("resolve the profile of calling session %s: %w", callerSessionID, err)
	}
	return d.liveLaunchProfile(profileID)
}
