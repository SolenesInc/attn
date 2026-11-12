package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

func (d *Daemon) handleStandbySession(msg *protocol.StandbySessionMessage) error {
	sessionID := protocol.TrimID(msg.SessionID)
	if d.store.Get(sessionID) == nil {
		return fmt.Errorf("unknown live session %s", sessionID)
	}
	if !msg.Until.After(time.Now()) {
		return fmt.Errorf("session %s: standby until %s is in the past; choose a future time", sessionID, msg.Until.Format(time.RFC3339))
	}
	request := prompts.RenderText("session", "standby-request", prompts.Values{
		"until": msg.Until.Local().Format(time.RFC3339),
	})
	attempt := d.placeDelivery(context.Background(), sessionID, sessionmonitor.ComposedInput(uuid.NewString(), request))
	if attempt.Stage != sessionmonitor.Placed {
		return fmt.Errorf("session %s: could not ask the agent to go on standby: %w", sessionID, attempt.Err)
	}
	return nil
}
