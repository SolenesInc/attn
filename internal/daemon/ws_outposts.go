package daemon

import (
	"fmt"
	"strings"
	"syscall"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) changeOutpost(action, id, name, address string) error {
	r := &d.outposts
	r.actions.Lock()
	defer r.actions.Unlock()
	if !d.outpostsEnabled() {
		return fmt.Errorf("outposts_enabled is off; run attn settings set outposts_enabled true before you %s an outpost", action)
	}
	if action == "add" {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("outpost name is required")
		}
		if err := validateOutpostAddress(address); err != nil {
			return err
		}
		row, err := d.store.AddOutpost(name, address)
		if err != nil {
			return fmt.Errorf("add outpost %s (%s): %w", name, address, err)
		}
		d.startOutpostDial(row)
		d.publishFact(FactOutpostChanged, row.ID, nil)
		return nil
	}
	rows, err := d.store.ListOutposts()
	if err != nil {
		return err
	}
	var row *store.Outpost
	for i := range rows {
		if rows[i].ID == id {
			row = &rows[i]
			break
		}
	}
	if row == nil {
		return fmt.Errorf("outpost %q does not exist", id)
	}
	if action == "remove" {
		_, _, reachable := d.outpostConn(id)
		for _, session := range d.store.List("") {
			if protocol.Deref(session.Host) != id {
				continue
			}
			closed := store.SessionClose{By: store.SessionClosedByUser, Reason: "outpost removed"}
			if reachable == nil {
				closing, err := d.beginSessionClose(session.ID, closed, nil)
				if err != nil {
					return err
				}
				if closing.teardown != nil {
					<-d.terminateSessionAsync(session.ID, syscall.SIGTERM, closing.teardown)
				}
			} else {
				d.closeSession(session.ID, closed)
			}
		}
	}
	d.stopOutpostDial(id)
	if action == "remove" {
		err = d.store.DeleteOutpost(id)
	} else {
		err = d.store.ClearOutpostPins(id)
	}
	if err != nil {
		d.startOutpostDial(*row)
		return err
	}
	if action == "repair" {
		row.HostKey, row.DaemonID = "", ""
		d.startOutpostDial(*row)
	} else {
		r.mu.Lock()
		delete(r.dialers, id)
		r.mu.Unlock()
	}
	d.publishFact(FactOutpostChanged, id, nil)
	return nil
}

func (d *Daemon) handleOutpostCommand(client *wsClient, action, id, name, address string) {
	d.life.Go("outpostCommand", func() {
		if err := d.changeOutpost(action, id, name, address); err != nil {
			d.sendCommandError(client, action+"_outpost", err.Error())
		}
	})
}

func (d *Daemon) outpostsUpdatedMessage() *protocol.OutpostsUpdatedMessage {
	message := &protocol.OutpostsUpdatedMessage{Event: protocol.EventOutpostsUpdated, Outposts: []protocol.OutpostInfo{}}
	rows, err := d.store.ListOutposts()
	if err != nil {
		d.logf("outposts snapshot: %v", err)
		return message
	}
	enabled := d.outpostsEnabled()
	r := &d.outposts
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range rows {
		info := protocol.OutpostInfo{ID: row.ID, Name: row.Name, Address: row.Address, State: protocol.OutpostStateConnecting, StateSince: row.AddedAt}
		if row.DaemonID != "" {
			info.DaemonID = protocol.Ptr(row.DaemonID)
			info.HostKeyFingerprint = protocol.Ptr((bridge.Pin{HostKey: row.HostKey}).Fingerprint())
		}
		run := r.dialers[row.ID]
		_, info.Reach = d.outpostReach(run)
		if run != nil {
			info.State = protocol.OutpostState(run.state)
			info.StateSince = run.since
			if run.build != "" {
				info.Build = protocol.Ptr(run.build)
			}
			if run.lastError != "" {
				info.LastError = protocol.Ptr(run.lastError)
				info.LastErrorAt = protocol.Ptr(run.lastErrorAt)
			}
		}
		if !enabled {
			info.State = protocol.OutpostStateLost
		}
		message.Outposts = append(message.Outposts, info)
	}
	return message
}

func (d *Daemon) projectOutpostsUpdated() {
	d.projectSnapshot(protocol.EventOutpostsUpdated, func() { d.wsHub.BroadcastValue(d.outpostsUpdatedMessage()) })
}
