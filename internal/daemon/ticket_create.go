package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// handleTicketCreate mints an unbound backlog ticket: an explicit id is pinned
// (hard fail if malformed or taken), otherwise the title slug is auto-suffixed.
func (d *Daemon) handleTicketCreate(conn net.Conn, msg *protocol.TicketCreateMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "ticket new: source_session_id is required")
		return
	}
	author := d.ticketActorIdentity(sourceSessionID)
	title := strings.TrimSpace(msg.Title)
	if title == "" {
		d.sendError(conn, "ticket new: title is required")
		return
	}
	desc := ""
	if msg.Description != nil {
		desc = strings.TrimSpace(*msg.Description)
	}
	explicitID := ""
	if msg.ID != nil {
		explicitID = strings.TrimSpace(*msg.ID)
	}
	now := time.Now()

	var created *store.Ticket
	if explicitID != "" {
		t, err := d.store.CreateTicket(store.Ticket{
			ID:          explicitID,
			Title:       title,
			Description: desc,
			Status:      store.TicketStatusTodo,
		}, author, now)
		if err != nil {
			d.sendError(conn, "ticket new: "+err.Error())
			return
		}
		created = t
	} else {
		t, err := d.createTicketWithUniqueSlug(store.Ticket{
			Title:       title,
			Description: desc,
			Status:      store.TicketStatusTodo,
		}, ticketSlug(title), author, "", nil, now)
		if err != nil {
			d.sendError(conn, "ticket new: "+err.Error())
			return
		}
		created = t
	}

	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		TicketCreateResult: &protocol.TicketCreateResult{
			TicketID: created.ID,
			Status:   protocol.TicketStatus(created.Status),
			Title:    created.Title,
		},
	})
	d.publishTicketFact(FactTicketCreated, created.ID)
}
