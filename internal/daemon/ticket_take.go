package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleTicketTake(conn net.Conn, msg *protocol.TicketTakeMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "ticket take: source_session_id is required")
		return
	}
	ticketID := strings.TrimSpace(msg.TicketID)
	if ticketID == "" {
		d.sendError(conn, "ticket take: ticket_id is required")
		return
	}
	ticket, err := d.store.GetTicket(ticketID)
	if err != nil {
		d.sendError(conn, "ticket take: "+err.Error())
		return
	}
	if ticket == nil {
		d.sendError(conn, "ticket take: ticket "+ticketID+" not found")
		return
	}
	previous := ticket.Assignee
	if previous == sourceSessionID {
		_ = json.NewEncoder(conn).Encode(protocol.Response{
			Ok: true,
			TicketTakeResult: &protocol.TicketTakeResult{
				TicketID: ticketID, PreviousAssignee: previous,
				UnreadCount: protocol.Ptr(d.targetTicketUnreadCount(sourceSessionID, ticketID)),
			},
		})
		return
	}
	confirm := msg.Confirm != nil && *msg.Confirm
	if previous != "" && !confirm {
		d.sendError(conn, "ticket take: ticket "+ticketID+" is already assigned to "+previous+"; pass --confirm to take it over")
		return
	}
	if err := d.store.AssignTicket(ticketID, sourceSessionID, d.ticketActorIdentity(sourceSessionID), time.Now()); err != nil {
		d.sendError(conn, "ticket take: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		TicketTakeResult: &protocol.TicketTakeResult{
			TicketID: ticketID, PreviousAssignee: previous,
			UnreadCount: protocol.Ptr(d.targetTicketUnreadCount(sourceSessionID, ticketID)),
		},
	})
	// Authored by the taker, so notifyTicketObservers excludes it (no self-nudge) and fans out to the other participants.
	d.notifyTicketObservers(ticketID)
	d.publishTicketFact(FactTicketAssigned, ticketID)
}
