package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleRenameSession(client *wsClient, msg *protocol.RenameSessionMessage) {
	d.sendRenameResult(client, protocol.CmdRenameSession, strings.TrimSpace(msg.SessionID), d.renameSession(msg))
}

// A hub forwards the rename to the daemon that owns the session and answers
// with the owner's verdict, so a refused name is never reported as applied.
func (d *Daemon) handleRenameSessionConn(conn net.Conn, msg *protocol.RenameSessionMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	if endpointID := d.sessionOwnerEndpoint(sessionID); endpointID != "" {
		payload, err := json.Marshal(msg)
		if err == nil {
			err = d.hubManager.ForwardSessionRename(context.Background(), endpointID, sessionID, payload)
		}
		if err != nil {
			d.sendError(conn, fmt.Sprintf("rename session %s on the endpoint owning it: %v", sessionID, err))
			return
		}
		d.sendOK(conn)
		return
	}
	if err := d.renameSession(msg); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)
}

func (d *Daemon) renameSession(msg *protocol.RenameSessionMessage) error {
	sessionID := strings.TrimSpace(msg.SessionID)
	label := strings.TrimSpace(msg.Label)
	if sessionID == "" {
		return fmt.Errorf("missing session_id")
	}
	if label == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if n := len([]rune(label)); n > maxSessionNameRunes {
		return fmt.Errorf("name %q is %d characters, over the %d-character limit", label, n, maxSessionNameRunes)
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	d.store.UpdateSessionLabel(sessionID, label)
	session.Label = label
	d.publishFact(FactSessionRenamed, sessionID, nil)
	return nil
}

func (d *Daemon) handleRenameWorkspace(client *wsClient, msg *protocol.RenameWorkspaceMessage) {
	workspaceID := strings.TrimSpace(msg.WorkspaceID)
	title := strings.TrimSpace(msg.Title)
	if workspaceID == "" {
		d.sendRenameResult(client, protocol.CmdRenameWorkspace, workspaceID, fmt.Errorf("missing workspace_id"))
		return
	}
	if title == "" {
		d.sendRenameResult(client, protocol.CmdRenameWorkspace, workspaceID, fmt.Errorf("name cannot be empty"))
		return
	}
	if d.workspaces == nil {
		d.sendRenameResult(client, protocol.CmdRenameWorkspace, workspaceID, fmt.Errorf("workspace registry unavailable"))
		return
	}
	if _, ok := d.workspaces.rename(workspaceID, title); !ok {
		d.sendRenameResult(client, protocol.CmdRenameWorkspace, workspaceID, fmt.Errorf("workspace not found: %s", workspaceID))
		return
	}
	d.store.UpdateWorkspaceTitle(workspaceID, title)
	d.publishFact(FactWorkspaceRenamed, workspaceID, nil)
	d.sendRenameResult(client, protocol.CmdRenameWorkspace, workspaceID, nil)
}

func (d *Daemon) sendRenameResult(client *wsClient, cmd, id string, err error) {
	result := protocol.RenameResultMessage{
		Event:   protocol.EventRenameResult,
		Cmd:     cmd,
		ID:      id,
		Success: err == nil,
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}
