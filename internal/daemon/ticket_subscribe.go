package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleTicketSubscribe(conn net.Conn, msg *protocol.TicketSubscribeMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "ticket subscribe: source_session_id is required")
		return
	}
	ticketID := strings.TrimSpace(msg.TicketID)
	if ticketID == "" {
		d.sendError(conn, "ticket subscribe: ticket_id is required")
		return
	}
	identity := d.ticketActorIdentity(sourceSessionID)
	if err := d.store.AddTicketSubscription(identity, ticketID, time.Now()); err != nil {
		d.sendError(conn, "ticket subscribe: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		TicketSubscribeResult: &protocol.TicketSubscribeResult{
			TicketID:    ticketID,
			UnreadCount: protocol.Ptr(d.targetTicketUnreadCount(sourceSessionID, ticketID)),
		},
	})
}

func (d *Daemon) handleTicketUnsubscribe(conn net.Conn, msg *protocol.TicketUnsubscribeMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "ticket unsubscribe: source_session_id is required")
		return
	}
	ticketID := strings.TrimSpace(msg.TicketID)
	if ticketID == "" {
		d.sendError(conn, "ticket unsubscribe: ticket_id is required")
		return
	}
	if err := d.store.RemoveTicketSubscription(d.ticketActorIdentity(sourceSessionID), ticketID); err != nil {
		d.sendError(conn, "ticket unsubscribe: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                      true,
		TicketUnsubscribeResult: &protocol.TicketUnsubscribeResult{TicketID: ticketID},
	})
}
