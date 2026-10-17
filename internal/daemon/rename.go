package daemon

import (
	"fmt"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleRenameSession(client *wsClient, msg *protocol.RenameSessionMessage) {
	d.sendRenameResult(client, protocol.CmdRenameSession, protocol.TrimID(msg.SessionID), d.renameSession(msg))
}

func (d *Daemon) handleRenameSessionConn(conn net.Conn, msg *protocol.RenameSessionMessage) {
	if err := d.renameSession(msg); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)
}

func (d *Daemon) renameSession(msg *protocol.RenameSessionMessage) error {
	sessionID := protocol.TrimID(msg.SessionID)
	label := strings.TrimSpace(msg.Label)
	if sessionID == "" {
		return fmt.Errorf("missing session_id")
	}
	if n := len([]rune(label)); n > maxSessionNameRunes {
		return fmt.Errorf("name %q is %d characters, over the %d-character limit", label, n, maxSessionNameRunes)
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	if label == "" && session.Agent != protocol.AgentShellValue {
		return fmt.Errorf("name cannot be empty for an agent; only shell terminals have automatic titles")
	}
	d.store.UpdateSessionLabel(sessionID, label)
	session.Label = label
	d.publishFact(FactSessionRenamed, string(sessionID), nil)
	d.linkRenamed(sessionID, label)
	return nil
}

func (d *Daemon) sendRenameResult(client *wsClient, cmd string, id protocol.SessionID, err error) {
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
