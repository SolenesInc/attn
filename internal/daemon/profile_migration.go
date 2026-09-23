package daemon

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profilemigration"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func protocolMigrationGroup(group profilemigration.GroupState, confirmed bool) (protocol.MigrationGroup, error) {
	treeJSON, err := layouttree.EncodeLayout(group.Tree)
	if err != nil {
		return protocol.MigrationGroup{}, err
	}
	desktop, err := protocolDesktop(profiles.Desktop{ID: group.DesktopID, Panes: group.Panes})
	if err != nil {
		return protocol.MigrationGroup{}, err
	}
	return protocol.MigrationGroup{
		GroupID:         group.ID,
		Title:           group.Title,
		Directory:       group.Directory,
		SourceDesktopID: group.DesktopID,
		Confirmed:       confirmed,
		TreeJson:        treeJSON,
		Panes:           desktop.Panes,
	}, nil
}

func protocolMigrationDesktop(desktop profilemigration.Desktop) (protocol.MigrationDraftDesktop, error) {
	out := protocol.MigrationDraftDesktop{Key: desktop.Key}
	if desktop.DesktopID != "" {
		out.DesktopID = protocol.Ptr(desktop.DesktopID)
	}
	if desktop.ShortcutSlot != 0 {
		out.ShortcutSlot = protocol.Ptr(desktop.ShortcutSlot)
	}
	if desktop.Tree != nil {
		encoded, err := json.Marshal(desktop.Tree)
		if err != nil {
			return out, err
		}
		out.TreeJson = string(encoded)
	}
	return out, nil
}

func protocolMigrationState(view store.ProfileMigrationView) (protocol.MigrationState, error) {
	state := protocol.MigrationState{
		Phase:               protocol.MigrationPhase(view.State.Phase),
		Revision:            int(view.State.Revision),
		ProfileID:           view.Manifest.ProfileID,
		Groups:              []protocol.MigrationGroup{},
		Desktops:            []protocol.MigrationDraftDesktop{},
		CanUndo:             view.Plan.CanUndo(),
		SuggestionAvailable: view.Plan.SuggestionAvailable(),
	}
	if !view.PlacementRequired() {
		state.CanUndo, state.SuggestionAvailable = false, false
		return state, nil
	}
	confirmed := make(map[string]bool, len(view.Plan.Confirmed))
	for _, id := range view.Plan.Confirmed {
		confirmed[id] = true
	}
	for _, group := range view.Live {
		wire, err := protocolMigrationGroup(group, confirmed[group.ID])
		if err != nil {
			return state, err
		}
		state.Groups = append(state.Groups, wire)
	}
	for _, desktop := range view.Plan.Desktops {
		wire, err := protocolMigrationDesktop(desktop)
		if err != nil {
			return state, err
		}
		state.Desktops = append(state.Desktops, wire)
	}
	return state, nil
}

func (d *Daemon) fillInitialMigrationPhase(event *protocol.InitialStateMessage) {
	view, err := d.store.ProfileMigration()
	if err != nil {
		var profileErr *profiles.Error
		if !errors.As(err, &profileErr) || profileErr.Code != profiles.CodeUnavailable {
			d.logf("initial state: reading the workspace migration: %v", err)
		}
		return
	}
	phase := protocol.MigrationPhase(view.State.Phase)
	event.MigrationPhase = &phase
}

func (d *Daemon) publishMigrationChanged(profileID string) {
	d.publishFact(FactProfileMigrationChanged, profileID, nil)
}

func (d *Daemon) runMigrationAction(client *wsClient, action, requestID string, run func() (store.ProfileMigrationView, func(), error)) {
	result := protocol.MigrationResultMessage{Event: protocol.EventMigrationResult, RequestID: requestID, Action: action}
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
	if err := d.requireHome("the workspace migration"); err != nil {
		fail(err)
		return
	}
	view, publish, err := run()
	if err != nil {
		fail(err)
		return
	}
	state, err := protocolMigrationState(view)
	if err != nil {
		fail(err)
		return
	}
	result.Success = true
	result.State = &state
	d.sendToClient(client, result)
	if publish != nil {
		publish()
	}
}

func (d *Daemon) editMigration(client *wsClient, action, requestID string, expectedRevision int, edit func(plan profilemigration.Plan, live []profilemigration.GroupState) (profilemigration.Plan, error)) {
	d.runMigrationAction(client, action, requestID, func() (store.ProfileMigrationView, func(), error) {
		view, err := d.store.EditProfileMigration(int64(expectedRevision), edit)
		return view, func() { d.publishMigrationChanged(view.Manifest.ProfileID) }, err
	})
}

func (d *Daemon) handleMigrationGet(client *wsClient, msg *protocol.MigrationGetMessage) {
	d.runMigrationAction(client, msg.Cmd, msg.RequestID, func() (store.ProfileMigrationView, func(), error) {
		view, err := d.store.ProfileMigration()
		return view, nil, err
	})
}

func (d *Daemon) handleMigrationKeep(client *wsClient, msg *protocol.MigrationKeepMessage) {
	d.editMigration(client, msg.Cmd, msg.RequestID, msg.ExpectedRevision, func(plan profilemigration.Plan, live []profilemigration.GroupState) (profilemigration.Plan, error) {
		return plan.Keep(live, msg.GroupIds)
	})
}

func (d *Daemon) handleMigrationMove(client *wsClient, msg *protocol.MigrationMoveMessage) {
	d.editMigration(client, msg.Cmd, msg.RequestID, msg.ExpectedRevision, func(plan profilemigration.Plan, live []profilemigration.GroupState) (profilemigration.Plan, error) {
		return plan.Move(live, msg.GroupID, msg.TargetKey, protocol.Deref(msg.AnchorGroupID), profilemigration.Edge(msg.Edge), protocol.Deref(msg.Share))
	})
}

func (d *Daemon) handleMigrationSuggest(client *wsClient, msg *protocol.MigrationSuggestMessage) {
	d.editMigration(client, msg.Cmd, msg.RequestID, msg.ExpectedRevision, func(plan profilemigration.Plan, live []profilemigration.GroupState) (profilemigration.Plan, error) {
		return plan.Suggest(live)
	})
}

func (d *Daemon) handleMigrationUndo(client *wsClient, msg *protocol.MigrationUndoMessage) {
	d.editMigration(client, msg.Cmd, msg.RequestID, msg.ExpectedRevision, func(plan profilemigration.Plan, _ []profilemigration.GroupState) (profilemigration.Plan, error) {
		return plan.Undo()
	})
}

func (d *Daemon) handleMigrationFinish(client *wsClient, msg *protocol.MigrationFinishMessage) {
	d.runMigrationAction(client, msg.Cmd, msg.RequestID, func() (store.ProfileMigrationView, func(), error) {
		finish, err := d.store.FinishProfileMigration(int64(msg.ExpectedRevision))
		if err != nil || !finish.Finished {
			return finish.View, nil, err
		}
		return finish.View, func() {
			d.publishArrangementChanged(finish.Profile.ID, profileArrangementChange{
				DesktopIDs:        desktopIDs(finish.Desktops...),
				DeletedDesktopIDs: finish.Deleted,
			})
			d.publishMigrationChanged(finish.Profile.ID)
		}, nil
	})
}

func (d *Daemon) projectMigrationChanged(ev bus.Event) {
	if ev.Name != FactProfileMigrationChanged {
		view, err := d.store.ProfileMigration()
		if err != nil || !view.PlacementRequired() {
			return
		}
	}
	d.projectSnapshot(protocol.EventMigrationChanged, func() {
		view, err := d.store.ProfileMigration()
		if err != nil {
			d.logf("migration projection: %v", err)
			return
		}
		state, err := protocolMigrationState(view)
		if err != nil {
			d.logf("migration projection: %v", err)
			return
		}
		d.wsHub.SendValueToMatchingClients(protocol.MigrationChangedMessage{Event: protocol.EventMigrationChanged, State: state}, nil)
	})
}
