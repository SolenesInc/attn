package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	cliErrorUnknownCommand = "unknown_command"
	cliErrorRefused        = "refused"
	cliErrorNoReply        = "no_reply"
)

func (d *Daemon) recordAnalytics(stream analytics.Stream, name string, props map[string]any) {
	if !d.analyticsEnabled() {
		return
	}
	if err := d.appendAnalytics(stream, name, props); err != nil {
		d.logf("analytics: %v", err)
	}
}

func (d *Daemon) appendAnalytics(stream analytics.Stream, name string, props any) error {
	encoded, err := json.Marshal(props)
	if err != nil {
		return fmt.Errorf("encode %s/%s props: %w", stream, name, err)
	}
	record := analytics.Record{Stream: stream, Name: name, At: time.Now().UTC(), Props: encoded}
	if err := d.store.AppendAnalyticsStreamRecord(record); err != nil {
		return fmt.Errorf("record %s/%s: %w", stream, name, err)
	}
	return nil
}

func (d *Daemon) handleRecordAnalytics(msg *protocol.RecordAnalyticsMessage, caller protocol.TerminalID) {
	stream := analytics.Stream(msg.Stream)
	if stream != analytics.StreamApp && stream != analytics.StreamCLI {
		d.logf("analytics: record_analytics refused stream %q: use app or cli", msg.Stream)
		return
	}
	props := map[string]any(msg.Props)
	if stream == analytics.StreamCLI {
		props = d.withAnalyticsCaller(props, caller)
	}
	d.recordAnalytics(stream, msg.Name, props)
}

func (d *Daemon) withAnalyticsCaller(props map[string]any, terminal protocol.TerminalID) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	props["caller"] = "human"
	if terminal == "" {
		return props
	}
	sessionID := d.sessionInTerminal(terminal)
	if sessionID == "" {
		return props
	}
	props["caller"] = "agent"
	if session := d.store.Get(sessionID); session != nil {
		props["harness"] = session.Agent
	}
	return props
}

type analyticsReplyConn struct {
	net.Conn
	answered func(failed bool)
	replied  bool
}

func (c *analyticsReplyConn) Write(p []byte) (int, error) {
	if !c.replied {
		c.replied = true
		var reply struct {
			Ok      *bool   `json:"ok"`
			Success *bool   `json:"success"`
			Error   *string `json:"error"`
		}
		failed := json.Unmarshal(p, &reply) == nil &&
			(protocol.Deref(reply.Error) != "" || (reply.Ok != nil && !*reply.Ok) || (reply.Success != nil && !*reply.Success))
		c.answered(failed)
	}
	return c.Conn.Write(p)
}

func socketFrameCaller(frame []byte) (cmd string, caller protocol.TerminalID) {
	var fields struct {
		Cmd string `json:"cmd"`
		protocol.SocketCaller
	}
	_ = json.Unmarshal(frame, &fields)
	return fields.Cmd, protocol.Deref(fields.CallerTerminalID)
}

func (d *Daemon) recordingCLICommand(conn net.Conn, frame []byte, parseErr error) (*analyticsReplyConn, func()) {
	reply := &analyticsReplyConn{Conn: conn}
	reply.answered = func(failed bool) {
		switch {
		case parseErr != nil:
			d.recordCLICommand(frame, cliErrorUnknownCommand)
		case failed:
			d.recordCLICommand(frame, cliErrorRefused)
		default:
			d.recordCLICommand(frame, "")
		}
	}
	unanswered := func() {
		if !reply.replied {
			d.recordCLICommand(frame, cliErrorNoReply)
		}
	}
	return reply, unanswered
}

func (d *Daemon) recordCLICommand(frame []byte, errorKind string) {
	cmd, caller := socketFrameCaller(frame)
	props := map[string]any{"ok": errorKind == ""}
	if errorKind != "" {
		props["error_kind"] = errorKind
	}
	d.recordAnalytics(analytics.StreamCLI, cmd, d.withAnalyticsCaller(props, caller))
}
