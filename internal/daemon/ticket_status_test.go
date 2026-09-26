package daemon

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func delegateBoundSession(t *testing.T, d *Daemon) string {
	t.Helper()
	backend := &fakeSpawnBackend{}
	_, chiefSessionID, _ := setupDelegationSource(t, d, backend)
	if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, chiefSessionID); err != nil {
		t.Fatalf("set chief role: %v", err)
	}
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(chiefSessionID),
		Brief:           protocol.Ptr("Migrate the store to X"),
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	bindLegacyTicket(t, d, result.SessionID, chiefSessionID)
	return result.SessionID
}

func bindLegacyTicket(t *testing.T, d *Daemon, sessionID, delegatorSessionID string) string {
	t.Helper()
	return bindLegacyTicketTitled(t, d, sessionID, delegatorSessionID, "Migrate the store to X")
}

func bindLegacyTicketTitled(t *testing.T, d *Daemon, sessionID, delegatorSessionID, title string) string {
	t.Helper()
	return bindLegacyTicketAs(t, d, sessionID, delegatorSessionID, title, true)
}

func bindLegacyTicketAs(t *testing.T, d *Daemon, sessionID, delegatorSessionID, title string, ownedByChiefRole bool) string {
	t.Helper()
	author := delegatorSessionID
	ownerRole := store.TicketRoleChiefOfStaff
	var subscribers []string
	if !ownedByChiefRole {
		author = d.ticketActorIdentity(delegatorSessionID)
		ownerRole = ""
		subscribers = []string{author, store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)}
	}
	session := d.store.Get(sessionID)
	if session == nil {
		t.Fatalf("session %s was not persisted", sessionID)
	}
	created, err := d.createTicketWithUniqueSlug(store.Ticket{
		Title:       title,
		Description: title,
		Status:      store.TicketStatusWorking,
		Assignee:    sessionID,
		Cwd:         session.Directory,
		LastAgentID: "codex",
	}, ticketSlug(title), author, ownerRole, subscribers, time.Now())
	if err != nil {
		t.Fatalf("bind legacy ticket: %v", err)
	}
	return created.ID
}

func callSetTicketStatus(t *testing.T, d *Daemon, sessionID, workState, comment string) protocol.Response {
	t.Helper()
	return callSetTicketStatusByID(t, d, sessionID, workState, comment, "")
}

func callSetTicketStatusByID(t *testing.T, d *Daemon, sessionID, workState, comment, ticketID string) protocol.Response {
	t.Helper()
	msg := &protocol.SetTicketStatusMessage{
		Cmd:             protocol.CmdSetTicketStatus,
		SourceSessionID: sessionID,
		WorkState:       protocol.DispatchWorkState(workState),
	}
	if comment != "" {
		msg.Comment = protocol.Ptr(comment)
	}
	if ticketID != "" {
		msg.TicketID = protocol.Ptr(ticketID)
	}
	server, clientConn := net.Pipe()
	go func() {
		d.handleSetTicketStatus(server, msg)
		_ = server.Close()
	}()
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode set-ticket-status response: %v", err)
	}
	_ = clientConn.Close()
	return resp
}
