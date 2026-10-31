package daemon

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

var errCrewRenameNeedsV2 = errors.New("renaming crew members is part of crew v2, which is off; `attn settings set crew_v2_enabled true` turns it on")

func (d *Daemon) crewRename(msg *protocol.CrewRenameMessage) (*protocol.CrewRenameResult, error) {
	if !d.crewV2Enabled() {
		return nil, errCrewRenameNeedsV2
	}
	member, err := d.crewAskedMember(askerOf(msg.SourceSessionID, msg.ProfileID), msg.Member)
	if err != nil {
		return nil, err
	}
	d.crewWakeMu.Lock()
	defer d.crewWakeMu.Unlock()
	name := strings.TrimSpace(msg.Name)
	validate := crew.ValidateName
	if member.Chief {
		validate = crew.ValidateChiefName
	}
	if err := validate(name); err != nil {
		return nil, err
	}
	previous, movedHome, err := d.renameCrewMember(member, name)
	if err != nil {
		return nil, err
	}
	d.publishArrangementChanged(member.ProfileID)
	d.logf("crew: member %s renamed from %s to %s", member.ID, previous, name)
	result := &protocol.CrewRenameResult{Member: member.ID, Name: name, PreviousName: previous, HomeDir: movedHome}
	if d.crewBindingLive(member) {
		if err := d.renameSession(&protocol.RenameSessionMessage{SessionID: member.BindingSession, Label: name}); err != nil {
			d.logf("crew: %s's session %s keeps its label: %v", name, member.BindingSession, err)
		} else {
			result.SessionID = protocol.Ptr(member.BindingSession)
		}
	}
	return result, nil
}

func (d *Daemon) renameCrewMember(asked crew.Member, name string) (previous string, movedHome *string, err error) {
	if asked.InDataDir() || strings.EqualFold(asked.Name, name) {
		previous, err = d.store.RenameCrewMember(asked.ID, name)
		if err == nil {
			d.publishFact(FactCrewUpdated, asked.ID, nil)
		}
		return previous, nil, err
	}
	if holder, taken, err := d.crewMemberIn(asked.ProfileID, name); err != nil {
		return "", nil, err
	} else if taken && holder.ID != asked.ID {
		return "", nil, fmt.Errorf("the name %q is already taken by %s in this profile", name, holder.Name)
	}
	member, doc, err := d.crewMember(asked.ID)
	if err != nil {
		return "", nil, err
	}
	moved, _, err := d.relocateCrewHome(member, doc.Rev, filepath.Dir(member.HomeDir(d.dataRoot)), name, func(c store.DocumentCommit) (store.DocumentWriteResult, error) {
		var written store.DocumentWriteResult
		previous, written, err = d.store.RenameCrewMemberMovingHome(member.ID, name, c, time.Now())
		return written, err
	})
	if err != nil {
		return "", nil, err
	}
	return previous, &moved.Home, nil
}

func (d *Daemon) handleCrewRename(conn net.Conn, msg *protocol.CrewRenameMessage) {
	result, err := d.crewRename(msg)
	if err != nil {
		d.sendCrewError(conn, "rename", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewRenameResult: result})
}

func (d *Daemon) handleCrewRenameWS(client *wsClient, msg *protocol.CrewRenameMessage) {
	result, err := d.crewRename(msg)
	response := protocol.CrewRenameResultMessage{Event: protocol.EventCrewRenameResult, RequestID: protocol.Deref(msg.RequestID), Success: err == nil, Result: result}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, response)
}
