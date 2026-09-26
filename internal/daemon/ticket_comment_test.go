package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type syncConn struct{ buf bytes.Buffer }

func (c *syncConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *syncConn) Write(p []byte) (int, error)      { return c.buf.Write(p) }
func (c *syncConn) Close() error                     { return nil }
func (c *syncConn) LocalAddr() net.Addr              { return nil }
func (c *syncConn) RemoteAddr() net.Addr             { return nil }
func (c *syncConn) SetDeadline(time.Time) error      { return nil }
func (c *syncConn) SetReadDeadline(time.Time) error  { return nil }
func (c *syncConn) SetWriteDeadline(time.Time) error { return nil }

func callTicketComment(t *testing.T, d *Daemon, sessionID, ticketID, comment string) protocol.Response {
	t.Helper()
	conn := &syncConn{}
	d.handleTicketComment(conn, &protocol.TicketCommentMessage{
		Cmd:             protocol.CmdTicketComment,
		SourceSessionID: sessionID,
		TicketID:        ticketID,
		Comment:         comment,
	})
	var resp protocol.Response
	if err := json.Unmarshal(conn.buf.Bytes(), &resp); err != nil {
		t.Fatalf("decode ticket-comment response: %v", err)
	}
	return resp
}
