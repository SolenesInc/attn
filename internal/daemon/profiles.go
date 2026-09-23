package daemon

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type profileActionOutcome struct {
	profile  *profiles.Profile
	desktops []profiles.Desktop
	paneID   string
	publish  func()
}

func (o profileActionOutcome) withProfile(profile profiles.Profile) profileActionOutcome {
	o.profile = &profile
	return o
}

func (o profileActionOutcome) withPaneID(paneID string) profileActionOutcome {
	o.paneID = paneID
	return o
}

func (c *wsClient) selectProfile(profileID string) {
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	c.selectedProfileID = profileID
}

func (c *wsClient) selectedProfile() string {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.selectedProfileID
}

func protocolProfile(profile profiles.Profile) protocol.Profile {
	out := protocol.Profile{
		ID:               profile.ID,
		Name:             profile.Name,
		CurrentDesktopID: profile.CurrentDesktopID,
		Revision:         int(profile.Revision),
	}
	if profile.LastUsedAt != "" {
		out.LastUsedAt = protocol.Ptr(profile.LastUsedAt)
	}
	return out
}

func protocolDesktop(desktop profiles.Desktop) (protocol.Desktop, error) {
	treeJSON := ""
	if !layouttree.LayoutEmpty(desktop.Tree) {
		encoded, err := layouttree.EncodeLayout(desktop.Tree)
		if err != nil {
			return protocol.Desktop{}, err
		}
		treeJSON = encoded
	}
	out := protocol.Desktop{
		ID:           desktop.ID,
		ProfileID:    desktop.ProfileID,
		Name:         desktop.Name,
		OrderKey:     desktop.OrderKey,
		TreeJson:     treeJSON,
		ActivePaneID: desktop.ActivePaneID,
		Panes:        make([]protocol.DesktopPane, 0, len(desktop.Panes)),
		Revision:     int(desktop.Revision),
	}
	if desktop.ShortcutSlot != 0 {
		out.ShortcutSlot = protocol.Ptr(desktop.ShortcutSlot)
	}
	for _, pane := range desktop.Panes {
		wire := protocol.DesktopPane{
			PaneID:    pane.PaneID,
			DesktopID: desktop.ID,
			Kind:      protocol.LayoutPaneKind(pane.Kind),
			SessionID: pane.SessionID,
			Title:     pane.Title,
			Status:    protocol.LayoutPaneStatus(pane.Status),
		}
		if pane.Error != "" {
			wire.Error = protocol.Ptr(pane.Error)
		}
		out.Panes = append(out.Panes, wire)
	}
	return out, nil
}

func protocolDesktops(desktops []profiles.Desktop) ([]protocol.Desktop, error) {
	out := make([]protocol.Desktop, 0, len(desktops))
	for _, desktop := range desktops {
		wire, err := protocolDesktop(desktop)
		if err != nil {
			return nil, err
		}
		out = append(out, wire)
	}
	return out, nil
}

func (d *Daemon) liveProtocolProfiles() ([]protocol.Profile, error) {
	live, err := d.store.ListProfiles(false)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.Profile, 0, len(live))
	for _, profile := range live {
		out = append(out, protocolProfile(profile))
	}
	return out, nil
}

func (d *Daemon) scopeClientToProfile(client *wsClient, requestedProfileID string) {
	requested := strings.TrimSpace(requestedProfileID)
	if requested != "" {
		if profile, err := d.store.GetProfile(requested); err == nil && !profile.Deleted() {
			client.selectProfile(profile.ID)
			return
		}
	}
	if profile, err := d.store.MostRecentlyUsedProfile(); err == nil {
		client.selectProfile(profile.ID)
		return
	}
	client.selectProfile("")
}

func (d *Daemon) sendInitialArrangement(client *wsClient, event *protocol.InitialStateMessage) {
	client.arrangementMu.Lock()
	defer client.arrangementMu.Unlock()
	shown := d.fillInitialProfileState(client, event)
	data, err := json.Marshal(event)
	if err != nil {
		d.logf("initial state: encoding: %v", err)
		return
	}
	if d.sendOutbound(client, outboundMessage{kind: messageKindText, payload: data}) {
		client.shownTiles = shown
	}
}

func (d *Daemon) fillInitialProfileState(client *wsClient, event *protocol.InitialStateMessage) []desktopMarkdownTile {
	live, err := d.liveProtocolProfiles()
	if err != nil {
		var profileErr *profiles.Error
		if !errors.As(err, &profileErr) || profileErr.Code != profiles.CodeUnavailable {
			d.logf("initial state: listing profiles: %v", err)
		}
		return nil
	}
	event.Profiles = live
	selected := client.selectedProfile()
	if selected == "" {
		return nil
	}
	profile, desktops, err := d.store.ProfileArrangement(selected)
	if err != nil {
		d.logf("initial state: reading the arrangement of profile %s: %v, so the client starts on no profile", selected, err)
		client.selectProfile("")
		return nil
	}
	wire, err := protocolDesktops(desktops)
	if err != nil {
		d.logf("initial state: encoding the arrangement of profile %s: %v, so the client starts on no profile", selected, err)
		client.selectProfile("")
		return nil
	}
	event.SelectedProfileID = protocol.Ptr(selected)
	event.Desktops = wire
	if d.requireHome("profiles and desktops") != nil {
		return nil
	}
	return markdownTilesOnCurrentDesktop(profile, desktops)
}

func (d *Daemon) runProfileAction(client *wsClient, action, requestID string, run func() (profileActionOutcome, error)) {
	result := protocol.ProfileActionResultMessage{Event: protocol.EventProfileActionResult, RequestID: requestID, Action: action}
	fail := func(err error) {
		result.Error = protocol.Ptr(err.Error())
		code := protocol.ProfileErrorCodeInternal
		var profileErr *profiles.Error
		var fenced *enrollment.FencedError
		switch {
		case errors.As(err, &profileErr):
			code = protocol.ProfileErrorCode(profileErr.Code)
		case errors.As(err, &fenced):
			code = protocol.ProfileErrorCodeUnavailable
		default:
			d.logf("%s failed: %v", action, err)
		}
		result.ErrorCode = &code
		d.sendToClient(client, result)
	}
	if strings.TrimSpace(requestID) == "" {
		fail(profiles.Errorf(profiles.CodeInvalid, "%s needs a request_id", action))
		return
	}
	if err := d.requireHome("profiles and desktops"); err != nil {
		fail(err)
		return
	}
	outcome, err := run()
	if err != nil {
		fail(err)
		return
	}
	if outcome.profile != nil {
		wire := protocolProfile(*outcome.profile)
		result.Profile = &wire
	}
	if result.Desktops, err = protocolDesktops(outcome.desktops); err != nil {
		fail(err)
		return
	}
	if outcome.paneID != "" {
		result.PaneID = protocol.Ptr(outcome.paneID)
	}
	result.Success = true
	d.sendToClient(client, result)
	if outcome.publish != nil {
		outcome.publish()
	}
}

func (d *Daemon) publishArrangementChanged(profileID string) {
	d.publishFact(FactProfileArrangementChanged, profileID, nil)
	d.nudgeDesktopTileContent()
}

func (d *Daemon) desktopChanged(desktop profiles.Desktop) profileActionOutcome {
	return profileActionOutcome{
		desktops: []profiles.Desktop{desktop},
		publish: func() {
			d.publishArrangementChanged(desktop.ProfileID)
		},
	}
}

func (d *Daemon) handleProfileCreate(client *wsClient, msg *protocol.ProfileCreateMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		profile, desktop, err := d.store.CreateProfile(msg.Name)
		return profileActionOutcome{profile: &profile, desktops: []profiles.Desktop{desktop}, publish: func() {
			d.publishFact(FactProfileCreated, profile.ID, nil)
		}}, err
	})
}

func (d *Daemon) handleProfileRename(client *wsClient, msg *protocol.ProfileRenameMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		profile, err := d.store.RenameProfile(msg.ProfileID, msg.Name, int64(msg.ExpectedRevision))
		return profileActionOutcome{profile: &profile, publish: func() {
			d.publishFact(FactProfileRenamed, profile.ID, nil)
		}}, err
	})
}

func (d *Daemon) handleProfileDelete(client *wsClient, msg *protocol.ProfileDeleteMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		deletion, err := d.store.DeleteProfile(msg.ProfileID, int64(msg.ExpectedRevision), msg.DestinationProfileID)
		if err != nil {
			return profileActionOutcome{}, err
		}
		d.wsHub.ForEachClient(func(scoped *wsClient) {
			if scoped.selectedProfile() == deletion.Deleted.ID {
				scoped.selectProfile(deletion.Destination.ID)
			}
		})
		return profileActionOutcome{publish: func() {
			d.publishFact(FactProfileDeleted, deletion.Deleted.ID, nil)
			d.publishArrangementChanged(deletion.Destination.ID)
		}}, nil
	})
}

func (d *Daemon) handleProfileSelect(client *wsClient, msg *protocol.ProfileSelectMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		profile, err := d.store.SelectProfile(msg.ProfileID)
		if err != nil {
			return profileActionOutcome{}, err
		}
		client.selectProfile(profile.ID)
		return profileActionOutcome{publish: func() {
			d.publishArrangementChanged(profile.ID)
		}}, nil
	})
}

func (d *Daemon) handleDesktopCreate(client *wsClient, msg *protocol.DesktopCreateMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		profile, desktop, err := d.store.CreateDesktop(msg.ProfileID, protocol.Deref(msg.Name), protocol.Deref(msg.ShortcutSlot), msg.ShortcutSlot == nil)
		return d.desktopChanged(desktop).withProfile(profile), err
	})
}

func (d *Daemon) handleDesktopRename(client *wsClient, msg *protocol.DesktopRenameMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		desktop, err := d.store.RenameDesktop(msg.DesktopID, msg.Name, int64(msg.ExpectedRevision))
		return d.desktopChanged(desktop), err
	})
}

func (d *Daemon) handleDesktopSetShortcutSlot(client *wsClient, msg *protocol.DesktopSetShortcutSlotMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		desktop, err := d.store.SetDesktopShortcutSlot(msg.DesktopID, protocol.Deref(msg.ShortcutSlot), int64(msg.ExpectedRevision))
		return d.desktopChanged(desktop), err
	})
}

func (d *Daemon) handleDesktopReorder(client *wsClient, msg *protocol.DesktopReorderMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		desktop, err := d.store.ReorderDesktop(msg.DesktopID, protocol.Deref(msg.PreviousDesktopID), protocol.Deref(msg.NextDesktopID), int64(msg.ExpectedRevision))
		return d.desktopChanged(desktop), err
	})
}

func (d *Daemon) handleDesktopDelete(client *wsClient, msg *protocol.DesktopDeleteMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		deletion, err := d.store.DeleteDesktop(msg.DesktopID, int64(msg.ExpectedRevision))
		return profileActionOutcome{profile: &deletion.Profile, publish: func() {
			d.publishArrangementChanged(deletion.Profile.ID)
		}}, err
	})
}

func (d *Daemon) handleDesktopSetCurrent(client *wsClient, msg *protocol.DesktopSetCurrentMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		profile, err := d.store.SetCurrentDesktop(msg.ProfileID, msg.DesktopID)
		return profileActionOutcome{profile: &profile, publish: func() {
			d.publishArrangementChanged(profile.ID)
		}}, err
	})
}

func (d *Daemon) handleDesktopSetActivePane(client *wsClient, msg *protocol.DesktopSetActivePaneMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		profile, desktop, err := d.store.SetActivePane(msg.DesktopID, msg.PaneID)
		return d.desktopChanged(desktop).withProfile(profile), err
	})
}

func layoutDirection(direction *protocol.LayoutSplitDirection) layouttree.Direction {
	if direction != nil && *direction == protocol.LayoutSplitDirectionHorizontal {
		return layouttree.DirectionHorizontal
	}
	return layouttree.DirectionVertical
}

func (d *Daemon) handleDesktopPlaceSession(client *wsClient, msg *protocol.DesktopPlaceSessionMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		title := ""
		if session := d.store.Get(msg.SessionID); session != nil {
			title = session.Label
		}
		desktop, paneID, err := d.store.PlaceSession(store.SessionPlacementRequest{
			DesktopID:        msg.DesktopID,
			ExpectedRevision: int64(msg.ExpectedRevision),
			SessionID:        msg.SessionID,
			AnchorPaneID:     protocol.Deref(msg.AnchorPaneID),
			Direction:        layoutDirection(msg.Direction),
			NewPaneShare:     protocol.Deref(msg.NewPaneShare),
			Title:            title,
			Status:           profiles.PaneStatusReady,
		})
		return d.desktopChanged(desktop).withPaneID(paneID), err
	})
}

func (d *Daemon) handleDesktopMoveLeaf(client *wsClient, msg *protocol.DesktopMoveLeafMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		direction, before := layoutDockEdge(msg.Edge)
		move, err := d.store.MoveLeaf(store.LeafMoveRequest{
			SourceDesktopID:        msg.SourceDesktopID,
			TargetDesktopID:        msg.TargetDesktopID,
			LeafID:                 msg.LeafID,
			AnchorID:               protocol.Deref(msg.AnchorID),
			Direction:              direction,
			Before:                 before,
			LeafShare:              protocol.Deref(msg.LeafShare),
			ExpectedSourceRevision: int64(msg.ExpectedSourceRevision),
			ExpectedTargetRevision: int64(msg.ExpectedTargetRevision),
		})
		changed := []profiles.Desktop{move.Source}
		if move.Target.ID != move.Source.ID {
			changed = append(changed, move.Target)
		}
		return profileActionOutcome{desktops: changed, publish: func() {
			d.publishArrangementChanged(move.Source.ProfileID)
		}}, err
	})
}

func layoutDockEdge(edge protocol.LayoutDockEdge) (layouttree.Direction, bool) {
	switch edge {
	case protocol.LayoutDockEdgeLeft:
		return layouttree.DirectionVertical, true
	case protocol.LayoutDockEdgeTop:
		return layouttree.DirectionHorizontal, true
	case protocol.LayoutDockEdgeBottom:
		return layouttree.DirectionHorizontal, false
	default:
		return layouttree.DirectionVertical, false
	}
}

func (d *Daemon) handleDesktopRemoveLeaf(client *wsClient, msg *protocol.DesktopRemoveLeafMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		desktop, err := d.store.RemoveLeaf(msg.DesktopID, msg.LeafID, int64(msg.ExpectedRevision))
		return d.desktopChanged(desktop), err
	})
}

func (d *Daemon) handleDesktopSetSplitRatio(client *wsClient, msg *protocol.DesktopSetSplitRatioMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		desktop, err := d.store.SetDesktopSplitRatio(msg.DesktopID, msg.SplitID, msg.Ratio, int64(msg.ExpectedRevision))
		return d.desktopChanged(desktop), err
	})
}

func (d *Daemon) projectProfilesChanged() {
	d.projectSnapshot(protocol.EventProfilesChanged, func() {
		live, err := d.liveProtocolProfiles()
		if err != nil {
			d.logf("profiles snapshot: %v", err)
			return
		}
		d.wsHub.SendValueToMatchingClients(protocol.ProfilesChangedMessage{Event: protocol.EventProfilesChanged, Profiles: live}, nil)
	})
}

func (d *Daemon) projectProfileArrangementChanged(ev bus.Event) {
	profile, desktops, err := d.store.ProfileArrangement(ev.Subject)
	if err != nil {
		d.logf("arrangement projection: reading profile %s: %v", ev.Subject, err)
		return
	}
	wire, err := protocolDesktops(desktops)
	if err != nil {
		d.logf("arrangement projection: encoding profile %s: %v", ev.Subject, err)
		return
	}
	message := protocol.ProfileArrangementChangedMessage{
		Event:    protocol.EventProfileArrangementChanged,
		Profile:  protocolProfile(profile),
		Desktops: wire,
	}
	shown := markdownTilesOnCurrentDesktop(profile, desktops)
	d.wsHub.SendArrangementToMatchingClients(message, func(client *wsClient) bool {
		return client.selectedProfile() == profile.ID
	}, func(*wsClient) []desktopMarkdownTile { return shown })
}
