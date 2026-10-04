package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) commandUsageScope(client *wsClient, requestID, profileID string) error {
	if err := d.requireHome("command usage"); err != nil {
		return err
	}
	if strings.TrimSpace(requestID) == "" {
		return fmt.Errorf("command usage needs a request_id")
	}
	if profileID == "" || profileID != client.selectedProfile() {
		return fmt.Errorf("command usage profile %q does not match the selected profile", profileID)
	}
	return nil
}

func (d *Daemon) handleGetCommandUsage(client *wsClient, msg *protocol.GetCommandUsageMessage) {
	result := protocol.GetCommandUsageResultMessage{
		Event: protocol.EventGetCommandUsageResult, RequestID: msg.RequestID, ProfileID: msg.ProfileID, Entries: []protocol.CommandUsage{},
	}
	err := d.commandUsageScope(client, msg.RequestID, msg.ProfileID)
	if err == nil {
		entries, readErr := d.store.GetCommandUsage(msg.ProfileID)
		err = readErr
		for _, entry := range entries {
			result.Entries = append(result.Entries, protocol.CommandUsage{CommandID: entry.CommandID, Score: entry.Score, LastUsedAt: entry.LastUsedAt.Format(time.RFC3339Nano)})
		}
	}
	result.Success = err == nil
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}

func (d *Daemon) handleRecordCommandUsage(client *wsClient, msg *protocol.RecordCommandUsageMessage) {
	result := protocol.RecordCommandUsageResultMessage{Event: protocol.EventRecordCommandUsageResult, RequestID: msg.RequestID}
	err := d.commandUsageScope(client, msg.RequestID, msg.ProfileID)
	if err == nil && strings.TrimSpace(msg.CommandID) == "" {
		err = fmt.Errorf("command usage needs a command_id")
	}
	if err == nil {
		err = d.store.RecordCommandUsage(msg.ProfileID, msg.CommandID)
	}
	result.Success = err == nil
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}
