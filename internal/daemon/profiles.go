package daemon

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/victorarias/attn/internal/crew"
	"path/filepath"
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type profileActionOutcome struct {
	profile  *profiles.Profile
	desktops []profiles.Desktop
	paneID   string
	moved    *protocol.LeafMoved
	// The action may change what the requester shows, so it gets its own answer.
	arranges bool
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

func (c *wsClient) profileOr(requested *string) *string {
	if strings.TrimSpace(protocol.Deref(requested)) != "" {
		return requested
	}
	if selected := c.selectedProfile(); selected != "" {
		return &selected
	}
	return requested
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
			RuntimeID: pane.RuntimeID,
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
	shown, seq := d.fillInitialProfileState(client, event)
	data, err := json.Marshal(event)
	if err != nil {
		d.logf("initial state: encoding: %v", err)
		return
	}
	if d.sendOutbound(client, outboundMessage{kind: messageKindText, payload: data}) {
		client.shownTiles = shown
		client.arrangementSeq = seq
	}
}

func (d *Daemon) fillInitialProfileState(client *wsClient, event *protocol.InitialStateMessage) ([]desktopMarkdownTile, int64) {
	live, err := d.liveProtocolProfiles()
	if err != nil {
		var profileErr *profiles.Error
		if !errors.As(err, &profileErr) || profileErr.Code != profiles.CodeUnavailable {
			d.logf("initial state: listing profiles: %v", err)
		}
		return nil, 0
	}
	event.Profiles = live
	selected := client.selectedProfile()
	if selected == "" {
		return nil, 0
	}
	profile, desktops, seq, err := d.store.ProfileArrangementSeq(selected)
	if err != nil {
		d.logf("initial state: reading the arrangement of profile %s: %v, so the client starts on no profile", selected, err)
		client.selectProfile("")
		return nil, 0
	}
	wire, err := protocolDesktops(desktops)
	if err != nil {
		d.logf("initial state: encoding the arrangement of profile %s: %v, so the client starts on no profile", selected, err)
		client.selectProfile("")
		return nil, 0
	}
	event.SelectedProfileID = protocol.Ptr(selected)
	event.Desktops = wire
	if d.requireHome("profiles and desktops") != nil {
		return nil, 0
	}
	return markdownTilesOnCurrentDesktop(profile, desktops), seq
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
	previousProfile := client.selectedProfile()
	client.holdArrangements()
	outcome, err := run()
	if pause := d.heldActionRan.Load(); pause != nil {
		(*pause)()
	}
	if err == nil && outcome.profile != nil {
		wire := protocolProfile(*outcome.profile)
		result.Profile = &wire
	}
	if err == nil {
		result.Desktops, err = protocolDesktops(outcome.desktops)
	}
	if err != nil {
		fail(err)
		d.releaseArrangements(client)
		return
	}
	if outcome.paneID != "" {
		result.PaneID = protocol.Ptr(outcome.paneID)
	}
	result.Success = true
	if outcome.arranges {
		d.sendArrangement(client, requestID, outcome.moved)
	}
	if previousProfile != client.selectedProfile() {
		d.sendGardenProfile(client)
	}
	d.sendToClient(client, result)
	d.releaseArrangements(client)
	if outcome.publish != nil {
		outcome.publish()
	}
}

// While a client's own request runs, broadcasts to it wait, so its change reaches it first as its own answer.
func (c *wsClient) holdArrangements() {
	c.arrangementMu.Lock()
	defer c.arrangementMu.Unlock()
	c.arrangementsHeld++
}

func (d *Daemon) releaseArrangements(client *wsClient) {
	client.arrangementMu.Lock()
	client.arrangementsHeld--
	missed := client.arrangementsHeld == 0 && client.arrangementsMissed
	if missed {
		client.arrangementsMissed = false
	}
	client.arrangementMu.Unlock()
	if missed {
		d.sendArrangement(client, "", nil)
	}
}

// sendArrangement sends the client its selected profile's arrangement now; a request id marks it as that request's answer.
func (d *Daemon) sendArrangement(client *wsClient, requestID string, moved *protocol.LeafMoved) {
	profileID := client.selectedProfile()
	if profileID == "" {
		return
	}
	message, data, delivery, ok := d.arrangementMessage(profileID, moved)
	if !ok {
		return
	}
	if requestID != "" {
		message.RequestID = protocol.Ptr(requestID)
		var err error
		if data, err = json.Marshal(message); err != nil {
			d.logf("arrangement answer: encoding profile %s: %v", profileID, err)
			return
		}
	}
	client.arrangementMu.Lock()
	defer client.arrangementMu.Unlock()
	if delivery.seq <= client.arrangementSeq || (requestID == "" && delivery.print == client.arrangementPrint) {
		return
	}
	if d.wsHub.deliverArrangementLocked(client, outboundMessage{kind: messageKindText, payload: data}, delivery) {
		d.wsHub.forget(client)
	}
}

// arrangementMessage reads a profile's arrangement once and encodes it once for every client it goes to.
func (d *Daemon) arrangementMessage(profileID string, moved *protocol.LeafMoved) (protocol.ProfileArrangementChangedMessage, []byte, *arrangementDelivery, bool) {
	profile, desktops, seq, err := d.store.ProfileArrangementSeq(profileID)
	if err != nil {
		d.logf("arrangement: reading profile %s: %v", profileID, err)
		return protocol.ProfileArrangementChangedMessage{}, nil, nil, false
	}
	wire, err := protocolDesktops(desktops)
	if err != nil {
		d.logf("arrangement: encoding profile %s: %v", profileID, err)
		return protocol.ProfileArrangementChangedMessage{}, nil, nil, false
	}
	message := protocol.ProfileArrangementChangedMessage{
		Event:     protocol.EventProfileArrangementChanged,
		Profile:   protocolProfile(profile),
		Desktops:  wire,
		MovedLeaf: moved,
	}
	data, err := json.Marshal(message)
	if err != nil {
		d.logf("arrangement: encoding profile %s: %v", profileID, err)
		return protocol.ProfileArrangementChangedMessage{}, nil, nil, false
	}
	return message, data, &arrangementDelivery{shown: markdownTilesOnCurrentDesktop(profile, desktops), seq: seq, print: sha256.Sum256(data)}, true
}

func (d *Daemon) publishArrangementChanged(profileID string) {
	d.publishArrangement(profileID, nil)
}

func (d *Daemon) publishArrangement(profileID string, payload any) {
	d.publishFact(FactProfileArrangementChanged, profileID, payload)
	d.nudgeDesktopTileContent()
	d.refreshCurrentAgent()
}

func (d *Daemon) desktopChanged(desktop profiles.Desktop) profileActionOutcome {
	return profileActionOutcome{
		desktops: []profiles.Desktop{desktop},
		arranges: true,
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
		remote, err := d.countRemoteProfileSessions(msg.ProfileID)
		if err != nil {
			return profileActionOutcome{}, err
		}
		homes, err := crew.ScanHomes(filepath.Join(d.dataRoot, crew.HomesDirName), d.logf)
		if err != nil {
			return profileActionOutcome{}, err
		}
		members := 0
		for _, member := range homes {
			owner, err := d.store.CrewProfile(member.ID)
			if err != nil {
				return profileActionOutcome{}, err
			}
			if owner == msg.ProfileID {
				members++
			}
		}
		d.automationMu.Lock()
		deleted, err := d.store.DeleteProfile(msg.ProfileID, int64(msg.ExpectedRevision), remote, members)
		d.automationMu.Unlock()
		if err != nil {
			return profileActionOutcome{}, err
		}
		remaining, err := d.store.MostRecentlyUsedProfile()
		if err != nil {
			return profileActionOutcome{}, err
		}
		d.wsHub.ForEachClient(func(scoped *wsClient) {
			if scoped.selectedProfile() == deleted.ID {
				scoped.selectProfile(remaining.ID)
				if scoped != client {
					d.sendArrangement(scoped, "", nil)
					d.sendGardenProfile(scoped)
				}
			}
		})
		return profileActionOutcome{arranges: true, publish: func() { d.publishFact(FactProfileDeleted, deleted.ID, nil) }}, nil
	})
}

func (d *Daemon) countRemoteProfileSessions(profileID string) (int, error) {
	if d.hubManager == nil {
		return 0, nil
	}
	counted := map[string]bool{}
	for _, session := range d.hubManager.RemoteSessions() {
		if d.store.Get(session.ID) != nil || counted[session.ID] {
			continue
		}
		owner, err := d.sessionProfileID(session.ID)
		if err != nil {
			return 0, err
		}
		if owner != profileID {
			continue
		}
		counted[session.ID] = true
	}
	return len(counted), nil
}

func (d *Daemon) handleProfileSelect(client *wsClient, msg *protocol.ProfileSelectMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		profile, err := d.store.SelectProfile(msg.ProfileID)
		if err != nil {
			return profileActionOutcome{}, err
		}
		client.selectProfile(profile.ID)
		return profileActionOutcome{arranges: true, publish: func() {
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

func (d *Daemon) handleDesktopReorder(client *wsClient, msg *protocol.DesktopReorderMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		desktop, err := d.store.ReorderDesktop(msg.DesktopID, protocol.Deref(msg.PreviousDesktopID), protocol.Deref(msg.NextDesktopID), int64(msg.ExpectedRevision))
		return d.desktopChanged(desktop), err
	})
}

func (d *Daemon) handleDesktopSetCurrent(client *wsClient, msg *protocol.DesktopSetCurrentMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		profile, err := d.store.SetCurrentDesktop(msg.ProfileID, msg.DesktopID)
		return profileActionOutcome{profile: &profile, arranges: true, publish: func() {
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

func (d *Daemon) leafShown(client *wsClient, profile profiles.Profile, desktop profiles.Desktop, leafID string) profileActionOutcome {
	client.selectProfile(profile.ID)
	return d.desktopChanged(desktop).withProfile(profile).withPaneID(leafID)
}

func (d *Daemon) handleDesktopShowSession(client *wsClient, msg *protocol.DesktopShowSessionMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		profile, desktop, leafID, err := d.store.ShowSession(msg.SessionID)
		if err != nil {
			return profileActionOutcome{}, err
		}
		return d.leafShown(client, profile, desktop, leafID), nil
	})
}

func (d *Daemon) handleDesktopShowLeaf(client *wsClient, msg *protocol.DesktopShowLeafMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		profile, desktop, leafID, err := d.store.ShowLeaf(msg.DesktopID, msg.LeafID)
		if err != nil {
			return profileActionOutcome{}, err
		}
		return d.leafShown(client, profile, desktop, leafID), nil
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
			Focus:            true,
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
			Activate:               true,
		})
		changed := []profiles.Desktop{move.Source}
		if move.Target.ID != move.Source.ID {
			changed = append(changed, move.Target)
		}
		moved := &protocol.LeafMoved{FromDesktopID: move.Source.ID, FromLeafID: msg.LeafID, ToDesktopID: move.Target.ID, ToLeafID: move.FinalLeafID}
		return profileActionOutcome{desktops: changed, paneID: move.FinalLeafID, moved: moved, arranges: true, publish: func() {
			d.publishArrangement(move.Source.ProfileID, moved)
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

func (d *Daemon) handleDesktopCloseTile(client *wsClient, msg *protocol.DesktopCloseTileMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		desktop, err := d.store.GetDesktop(msg.DesktopID)
		if err != nil {
			return profileActionOutcome{}, err
		}
		i := slices.IndexFunc(desktop.Panes, func(pane profiles.Pane) bool { return pane.PaneID == msg.TileID })
		if i < 0 {
			return profileActionOutcome{}, profiles.Errorf(profiles.CodeNotFound, "desktop %s has no terminal tile %s", msg.DesktopID, msg.TileID)
		}
		tile := desktop.Panes[i]
		if d.closeTerminal(tile.SessionID, harness.TerminalID(tile.RuntimeID)) {
			closing, err := d.beginUserSessionClose(tile.SessionID, store.SessionClose{By: store.SessionClosedByUser}, client)
			if err != nil {
				return profileActionOutcome{}, err
			}
			d.finishSessionClose(tile.SessionID, closing)
		} else {
			d.detachSession(client, tile.RuntimeID)
		}
		desktop, err = d.store.GetDesktop(msg.DesktopID)
		return profileActionOutcome{desktops: []profiles.Desktop{desktop}, arranges: true}, err
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
	var moved *protocol.LeafMoved
	if len(ev.Payload) > 0 {
		var decoded protocol.LeafMoved
		if err := ev.Decode(&decoded); err != nil {
			d.logf("arrangement projection: decoding the moved leaf of profile %s: %v", ev.Subject, err)
		} else {
			moved = &decoded
		}
	}
	_, data, delivery, ok := d.arrangementMessage(ev.Subject, moved)
	if !ok {
		return
	}
	d.wsHub.SendArrangementToMatchingClients(data, func(client *wsClient) bool {
		return client.selectedProfile() == ev.Subject
	}, delivery)
}
