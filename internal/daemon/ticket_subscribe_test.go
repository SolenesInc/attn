package daemon

import (
	"encoding/json"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func callTicketSubscribe(t *testing.T, d *Daemon, sessionID, ticketID string) protocol.Response {
	t.Helper()
	conn := &syncConn{}
	d.handleTicketSubscribe(conn, &protocol.TicketSubscribeMessage{
		Cmd:             protocol.CmdTicketSubscribe,
		SourceSessionID: sessionID,
		TicketID:        ticketID,
	})
	var resp protocol.Response
	if err := json.Unmarshal(conn.buf.Bytes(), &resp); err != nil {
		t.Fatalf("decode ticket-subscribe response: %v", err)
	}
	return resp
}
