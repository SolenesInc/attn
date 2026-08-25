package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// `crashed` and `todo` are intentionally unreachable here: crashed is attn-authored when an agent dies without
// reporting, and todo is the pre-assignment backlog state.
func ticketStatusFromWorkState(ws protocol.DispatchWorkState) (store.TicketStatus, bool) {
	switch ws {
	case protocol.DispatchWorkStateInProgress:
		return store.TicketStatusWorking, true
	case protocol.DispatchWorkStateNeedsInput:
		return store.TicketStatusBlocked, true
	case protocol.DispatchWorkStateReadyForReview:
		return store.TicketStatusInReview, true
	case protocol.DispatchWorkStateCompleted:
		return store.TicketStatusDone, true
	case protocol.DispatchWorkStateFailed:
		return store.TicketStatusFailed, true
	default:
		return "", false
	}
}

// With an explicit ticket id there is deliberately NO ownership gate: that form
// is for awareness (the chief or a peer nudging the board).
func (d *Daemon) handleSetTicketStatus(conn net.Conn, msg *protocol.SetTicketStatusMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "ticket status: source_session_id is required")
		return
	}
	status, ok := ticketStatusFromWorkState(msg.WorkState)
	if !ok {
		d.sendError(conn, fmt.Sprintf("ticket status: unknown work state %q", msg.WorkState))
		return
	}
	ticketID := strings.TrimSpace(protocol.Deref(msg.TicketID))
	if ticketID == "" {
		ticket, err := d.store.ActiveTicketForSession(sourceSessionID)
		if err != nil {
			d.sendError(conn, "ticket status: "+err.Error())
			return
		}
		if ticket == nil {
			d.sendError(conn, "ticket status: no active ticket bound to this session")
			return
		}
		ticketID = ticket.ID
	}
	comment := ""
	if msg.Comment != nil {
		comment = strings.TrimSpace(*msg.Comment)
	}
	d.deliveryMu.Lock()
	author := d.ticketActorIdentity(sourceSessionID)
	updated, outcome, err := d.store.SetTicketStatusWithOptions(
		ticketID, status, author, comment,
		d.ticketMutationOptions(sourceSessionID), time.Now(),
	)
	if err != nil {
		d.deliveryMu.Unlock()
		d.sendError(conn, "ticket status: "+err.Error())
		return
	}
	result := &protocol.TicketStatusResult{
		TicketID: ticketID,
		Status:   protocol.TicketStatus(status),
		CatchUp:  ticketMutationCatchUp(ticketID, outcome.CatchUp),
		Applied:  !outcome.Blocked,
	}
	d.afterTicketMutationCatchUpLocked(sourceSessionID, outcome.CatchUp)
	if outcome.Blocked {
		if current, getErr := d.store.GetTicket(ticketID); getErr == nil && current != nil {
			result.Status = protocol.TicketStatus(current.Status)
		}
		d.deliveryMu.Unlock()
		_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, TicketStatusResult: result})
		return
	}
	result.TicketID = updated.ID
	result.Status = protocol.TicketStatus(updated.Status)
	d.deliveryMu.Unlock()
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                 true,
		TicketStatusResult: result,
	})
	d.mirrorStatusOntoSeed(sourceSessionID, updated, msg.WorkState, comment)
	// Notify excludes the event's author, so this never self-nudges the mover.
	d.notifyTicketObservers(updated.ID)
	d.publishTicketFact(FactTicketStatusChanged, updated.ID)
}

// A failed mirror is logged, never returned: the ticket already moved.
func (d *Daemon) mirrorStatusOntoSeed(sessionID string, ticket *store.Ticket, state protocol.DispatchWorkState, comment string) {
	if ticket == nil || ticket.Assignee != sessionID {
		return
	}
	seedID, ok := d.gardenDispatchCrown(sessionID)
	if !ok {
		return
	}
	if _, err := d.appendSeedNote(seedID, statusNoteBody(state, comment), sessionID, "", garden.NoteKindNote, nil); err != nil {
		d.logf("garden: mirroring %s onto seed %s: %v", state, seedID, err)
	}
}

func statusNoteBody(state protocol.DispatchWorkState, comment string) string {
	line := fmt.Sprintf("reported %s", state)
	if comment = strings.TrimSpace(comment); comment != "" {
		return line + "\n\n" + comment
	}
	return line
}
