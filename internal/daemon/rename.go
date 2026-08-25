package daemon

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleRenameSession(client *wsClient, msg *protocol.RenameSessionMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	label := strings.TrimSpace(msg.Label)
	if sessionID == "" {
		d.sendRenameResult(client, protocol.CmdRenameSession, sessionID, fmt.Errorf("missing session_id"))
		return
	}
	if label == "" {
		d.sendRenameResult(client, protocol.CmdRenameSession, sessionID, fmt.Errorf("name cannot be empty"))
		return
	}
	session := d.store.Get(sessionID)
	if session == nil {
		d.sendRenameResult(client, protocol.CmdRenameSession, sessionID, fmt.Errorf("session not found: %s", sessionID))
		return
	}
	d.store.UpdateSessionLabel(sessionID, label)
	session.Label = label
	d.publishFact(FactSessionRenamed, sessionID, nil)
	d.sendRenameResult(client, protocol.CmdRenameSession, sessionID, nil)
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
