package daemon

import (
	"errors"
	"strings"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setups"
	"github.com/victorarias/attn/internal/store"
)

type setupArrangementChange struct {
	DesktopIDs        []string `json:"desktop_ids,omitempty"`
	DeletedDesktopIDs []string `json:"deleted_desktop_ids,omitempty"`
}

type setupActionOutcome struct {
	setup    *setups.Setup
	desktops []setups.Desktop
	paneID   string
	publish  func()
}

func (c *wsClient) selectSetup(setupID string) {
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	c.selectedSetupID = setupID
}

func (c *wsClient) selectedSetup() string {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.selectedSetupID
}

func protocolSetup(setup setups.Setup) protocol.Setup {
	out := protocol.Setup{
		ID:               setup.ID,
		Name:             setup.Name,
		CurrentDesktopID: setup.CurrentDesktopID,
		Revision:         int(setup.Revision),
	}
	if setup.LastUsedAt != "" {
		out.LastUsedAt = protocol.Ptr(setup.LastUsedAt)
	}
	return out
}

func protocolDesktop(desktop setups.Desktop) (protocol.Desktop, error) {
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
		SetupID:      desktop.SetupID,
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

func protocolDesktops(desktops []setups.Desktop) ([]protocol.Desktop, error) {
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

func (d *Daemon) liveProtocolSetups() ([]protocol.Setup, error) {
	live, err := d.store.ListSetups(false)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.Setup, 0, len(live))
	for _, setup := range live {
		out = append(out, protocolSetup(setup))
	}
	return out, nil
}

func (d *Daemon) scopeClientToSetup(client *wsClient, requestedSetupID string) {
	requested := strings.TrimSpace(requestedSetupID)
	if requested != "" {
		if setup, err := d.store.GetSetup(requested); err == nil && !setup.Deleted() {
			client.selectSetup(setup.ID)
			return
		}
	}
	if setup, err := d.store.MostRecentlyUsedSetup(); err == nil {
		client.selectSetup(setup.ID)
		return
	}
	client.selectSetup("")
}

func (d *Daemon) fillInitialSetupState(client *wsClient, event *protocol.InitialStateMessage) {
	live, err := d.liveProtocolSetups()
	if err != nil {
		var setupErr *setups.Error
		if !errors.As(err, &setupErr) || setupErr.Code != setups.CodeUnavailable {
			d.logf("initial state: listing setups: %v", err)
		}
		return
	}
	event.Setups = live
	selected := client.selectedSetup()
	if selected == "" {
		return
	}
	_, desktops, err := d.store.SetupArrangement(selected)
	if err != nil {
		d.logf("initial state: reading the arrangement of setup %s, so the client starts on no setup: %v", selected, err)
		client.selectSetup("")
		return
	}
	wire, err := protocolDesktops(desktops)
	if err != nil {
		d.logf("initial state: encoding the arrangement of setup %s, so the client starts on no setup: %v", selected, err)
		client.selectSetup("")
		return
	}
	event.SelectedSetupID = protocol.Ptr(selected)
	event.Desktops = wire
}

func (d *Daemon) runSetupAction(client *wsClient, action, requestID string, run func() (setupActionOutcome, error)) {
	result := protocol.SetupActionResultMessage{Event: protocol.EventSetupActionResult, RequestID: requestID, Action: action}
	fail := func(err error) {
		result.Error = protocol.Ptr(err.Error())
		code := protocol.SetupErrorCodeInternal
		var setupErr *setups.Error
		var fenced *enrollment.FencedError
		switch {
		case errors.As(err, &setupErr):
			code = protocol.SetupErrorCode(setupErr.Code)
		case errors.As(err, &fenced):
			code = protocol.SetupErrorCodeUnavailable
		default:
			d.logf("%s failed: %v", action, err)
		}
		result.ErrorCode = &code
		d.sendToClient(client, result)
	}
	if strings.TrimSpace(requestID) == "" {
		fail(setups.Errorf(setups.CodeInvalid, "%s needs a request_id", action))
		return
	}
	if err := d.requireHome("setups and desktops"); err != nil {
		fail(err)
		return
	}
	outcome, err := run()
	if err != nil {
		fail(err)
		return
	}
	if outcome.setup != nil {
		wire := protocolSetup(*outcome.setup)
		result.Setup = &wire
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

func (d *Daemon) publishArrangementChanged(setupID string, change setupArrangementChange) {
	d.publishFact(FactSetupArrangementChanged, setupID, change)
}

func desktopIDs(desktops ...setups.Desktop) []string {
	ids := make([]string, 0, len(desktops))
	for _, desktop := range desktops {
		if len(ids) > 0 && ids[len(ids)-1] == desktop.ID {
			continue
		}
		ids = append(ids, desktop.ID)
	}
	return ids
}

func (d *Daemon) desktopChanged(desktop setups.Desktop) setupActionOutcome {
	return setupActionOutcome{
		desktops: []setups.Desktop{desktop},
		publish: func() {
			d.publishArrangementChanged(desktop.SetupID, setupArrangementChange{DesktopIDs: desktopIDs(desktop)})
		},
	}
}

func (d *Daemon) handleSetupCreate(client *wsClient, msg *protocol.SetupCreateMessage) {
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
		setup, desktop, err := d.store.CreateSetup(msg.Name)
		return setupActionOutcome{setup: &setup, desktops: []setups.Desktop{desktop}, publish: func() {
			d.publishFact(FactSetupCreated, setup.ID, nil)
		}}, err
	})
}

func (d *Daemon) handleSetupRename(client *wsClient, msg *protocol.SetupRenameMessage) {
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
		setup, err := d.store.RenameSetup(msg.SetupID, msg.Name, int64(msg.ExpectedRevision))
		return setupActionOutcome{setup: &setup, publish: func() {
			d.publishFact(FactSetupRenamed, setup.ID, nil)
		}}, err
	})
}

func (d *Daemon) handleSetupDelete(client *wsClient, msg *protocol.SetupDeleteMessage) {
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
		deletion, err := d.store.DeleteSetup(msg.SetupID, int64(msg.ExpectedRevision), msg.DestinationSetupID)
		if err != nil {
			return setupActionOutcome{}, err
		}
		_, destination, err := d.store.SetupArrangement(deletion.Destination.ID)
		if err != nil {
			return setupActionOutcome{}, err
		}
		d.wsHub.ForEachClient(func(scoped *wsClient) {
			if scoped.selectedSetup() == deletion.Deleted.ID {
				scoped.selectSetup(deletion.Destination.ID)
			}
		})
		return setupActionOutcome{setup: &deletion.Destination, desktops: destination, publish: func() {
			d.publishFact(FactSetupDeleted, deletion.Deleted.ID, nil)
			d.publishArrangementChanged(deletion.Destination.ID, setupArrangementChange{DesktopIDs: desktopIDs(destination...)})
		}}, nil
	})
}

func (d *Daemon) handleSetupSelect(client *wsClient, msg *protocol.SetupSelectMessage) {
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
		setup, desktops, err := d.store.SelectSetup(msg.SetupID)
		if err != nil {
			return setupActionOutcome{}, err
		}
		client.selectSetup(setup.ID)
		return setupActionOutcome{setup: &setup, desktops: desktops, publish: func() {
			d.publishArrangementChanged(setup.ID, setupArrangementChange{})
		}}, nil
	})
}

func (d *Daemon) handleDesktopCreate(client *wsClient, msg *protocol.DesktopCreateMessage) {
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
		setup, desktop, err := d.store.CreateDesktop(msg.SetupID, protocol.Deref(msg.Name), protocol.Deref(msg.ShortcutSlot), msg.ShortcutSlot == nil)
		outcome := d.desktopChanged(desktop)
		outcome.setup = &setup
		return outcome, err
	})
}

func (d *Daemon) handleDesktopRename(client *wsClient, msg *protocol.DesktopRenameMessage) {
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
		desktop, err := d.store.RenameDesktop(msg.DesktopID, msg.Name, int64(msg.ExpectedRevision))
		return d.desktopChanged(desktop), err
	})
}

func (d *Daemon) handleDesktopSetShortcutSlot(client *wsClient, msg *protocol.DesktopSetShortcutSlotMessage) {
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
		desktop, err := d.store.SetDesktopShortcutSlot(msg.DesktopID, protocol.Deref(msg.ShortcutSlot), int64(msg.ExpectedRevision))
		return d.desktopChanged(desktop), err
	})
}

func (d *Daemon) handleDesktopReorder(client *wsClient, msg *protocol.DesktopReorderMessage) {
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
		desktop, err := d.store.ReorderDesktop(msg.DesktopID, protocol.Deref(msg.PreviousDesktopID), protocol.Deref(msg.NextDesktopID), int64(msg.ExpectedRevision))
		return d.desktopChanged(desktop), err
	})
}

func (d *Daemon) handleDesktopDelete(client *wsClient, msg *protocol.DesktopDeleteMessage) {
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
		deletion, err := d.store.DeleteDesktop(msg.DesktopID, int64(msg.ExpectedRevision))
		return setupActionOutcome{setup: &deletion.Setup, publish: func() {
			d.publishArrangementChanged(deletion.Setup.ID, setupArrangementChange{DeletedDesktopIDs: []string{deletion.Deleted.ID}})
		}}, err
	})
}

func (d *Daemon) handleDesktopSetCurrent(client *wsClient, msg *protocol.DesktopSetCurrentMessage) {
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
		setup, err := d.store.SetCurrentDesktop(msg.SetupID, msg.DesktopID)
		return setupActionOutcome{setup: &setup, publish: func() {
			d.publishArrangementChanged(setup.ID, setupArrangementChange{})
		}}, err
	})
}

func (d *Daemon) handleDesktopSetActivePane(client *wsClient, msg *protocol.DesktopSetActivePaneMessage) {
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
		setup, desktop, err := d.store.SetActivePane(msg.DesktopID, msg.PaneID)
		outcome := d.desktopChanged(desktop)
		outcome.setup = &setup
		return outcome, err
	})
}

func layoutDirection(direction *protocol.LayoutSplitDirection) layouttree.Direction {
	if direction != nil && *direction == protocol.LayoutSplitDirectionHorizontal {
		return layouttree.DirectionHorizontal
	}
	return layouttree.DirectionVertical
}

func (d *Daemon) handleDesktopPlaceSession(client *wsClient, msg *protocol.DesktopPlaceSessionMessage) {
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
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
			Status:           setups.PaneStatusReady,
		})
		outcome := d.desktopChanged(desktop)
		outcome.paneID = paneID
		return outcome, err
	})
}

func (d *Daemon) handleDesktopMoveLeaf(client *wsClient, msg *protocol.DesktopMoveLeafMessage) {
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
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
		changed := []setups.Desktop{move.Source}
		if move.Target.ID != move.Source.ID {
			changed = append(changed, move.Target)
		}
		return setupActionOutcome{desktops: changed, publish: func() {
			d.publishArrangementChanged(move.Source.SetupID, setupArrangementChange{DesktopIDs: desktopIDs(changed...)})
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
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
		desktop, err := d.store.RemoveLeaf(msg.DesktopID, msg.LeafID, int64(msg.ExpectedRevision))
		return d.desktopChanged(desktop), err
	})
}

func (d *Daemon) handleDesktopSetSplitRatio(client *wsClient, msg *protocol.DesktopSetSplitRatioMessage) {
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
		desktop, err := d.store.SetDesktopSplitRatio(msg.DesktopID, msg.SplitID, msg.Ratio, int64(msg.ExpectedRevision))
		return d.desktopChanged(desktop), err
	})
}

func (d *Daemon) projectSetupsChanged() {
	d.projectSnapshot(protocol.EventSetupsChanged, func() {
		live, err := d.liveProtocolSetups()
		if err != nil {
			d.logf("setups snapshot: %v", err)
			return
		}
		d.wsHub.SendValueToMatchingClients(protocol.SetupsChangedMessage{Event: protocol.EventSetupsChanged, Setups: live}, nil)
	})
}

func (d *Daemon) projectSetupArrangementChanged(ev bus.Event) {
	change, ok := decodeFact[setupArrangementChange](d, ev)
	if !ok {
		return
	}
	setup, err := d.store.GetSetup(ev.Subject)
	if err != nil {
		d.logf("arrangement projection: reading setup %s: %v", ev.Subject, err)
		return
	}
	message := protocol.SetupArrangementChangedMessage{
		Event:             protocol.EventSetupArrangementChanged,
		Setup:             protocolSetup(setup),
		Desktops:          make([]protocol.Desktop, 0, len(change.DesktopIDs)),
		DeletedDesktopIds: change.DeletedDesktopIDs,
	}
	for _, id := range change.DesktopIDs {
		desktop, err := d.store.GetDesktop(id)
		if err != nil {
			var setupErr *setups.Error
			if errors.As(err, &setupErr) && setupErr.Code == setups.CodeNotFound {
				message.DeletedDesktopIds = append(message.DeletedDesktopIds, id)
				continue
			}
			d.logf("arrangement projection: reading desktop %s: %v", id, err)
			return
		}
		wire, err := protocolDesktop(desktop)
		if err != nil {
			d.logf("arrangement projection: encoding desktop %s: %v", id, err)
			return
		}
		message.Desktops = append(message.Desktops, wire)
	}
	d.wsHub.SendValueToMatchingClients(message, func(client *wsClient) bool {
		return client.selectedSetup() == setup.ID
	})
}
