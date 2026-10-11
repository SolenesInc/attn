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
	d.publishFact(FactSessionRenamed, string(sessionID), nil)
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
