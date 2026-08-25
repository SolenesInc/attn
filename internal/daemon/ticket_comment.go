package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// handleTicketComment posts a comment on any ticket by id, authored as the calling
// session. Comment authorship is not a participation source (store.UnreadTicketEvents).
func (d *Daemon) handleTicketComment(conn net.Conn, msg *protocol.TicketCommentMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "ticket comment: source_session_id is required")
		return
	}
	ticketID := strings.TrimSpace(msg.TicketID)
	if ticketID == "" {
		d.sendError(conn, "ticket comment: ticket_id is required")
		return
	}
	comment := strings.TrimSpace(msg.Comment)
	if comment == "" {
		d.sendError(conn, "ticket comment: comment is required")
		return
	}
	d.deliveryMu.Lock()
	author := d.ticketActorIdentity(sourceSessionID)
	_, outcome, err := d.store.AddTicketCommentWithOptions(
		ticketID, author, comment,
		d.ticketMutationOptions(sourceSessionID), time.Now(),
	)
	if err != nil {
		d.deliveryMu.Unlock()
		d.sendError(conn, "ticket comment: "+err.Error())
		return
	}
	result := &protocol.TicketCommentResult{
		TicketID: ticketID,
		CatchUp:  ticketMutationCatchUp(ticketID, outcome.CatchUp),
		Applied:  !outcome.Blocked,
	}
	d.afterTicketMutationCatchUpLocked(sourceSessionID, outcome.CatchUp)
	if outcome.Blocked {
		d.deliveryMu.Unlock()
		_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, TicketCommentResult: result})
		return
	}
	d.deliveryMu.Unlock()
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, TicketCommentResult: result})
	d.notifyTicketObservers(ticketID)
	d.publishTicketFact(FactTicketCommented, ticketID)
}
