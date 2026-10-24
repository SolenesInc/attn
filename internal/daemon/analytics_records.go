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
	replied bool
	failed  bool
}

func (c *analyticsReplyConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if !c.replied {
		c.replied = true
		var reply struct {
			Ok      *bool   `json:"ok"`
			Success *bool   `json:"success"`
			Error   *string `json:"error"`
		}
		if json.Unmarshal(p, &reply) == nil {
			c.failed = protocol.Deref(reply.Error) != "" || (reply.Ok != nil && !*reply.Ok) || (reply.Success != nil && !*reply.Success)
		}
	}
	return n, err
}

func socketFrameCaller(frame []byte) (cmd string, caller protocol.TerminalID) {
	var fields struct {
		Cmd string `json:"cmd"`
		protocol.SocketCaller
	}
	_ = json.Unmarshal(frame, &fields)
	return fields.Cmd, protocol.Deref(fields.CallerTerminalID)
}

func (d *Daemon) recordCLICommand(frame []byte, reply *analyticsReplyConn, parseErr error) {
	cmd, caller := socketFrameCaller(frame)
	props := map[string]any{"ok": true}
	switch {
	case parseErr != nil:
		props["ok"], props["error_kind"] = false, cliErrorUnknownCommand
	case !reply.replied:
		props["ok"], props["error_kind"] = false, cliErrorNoReply
	case reply.failed:
		props["ok"], props["error_kind"] = false, cliErrorRefused
	}
	d.recordAnalytics(analytics.StreamCLI, cmd, d.withAnalyticsCaller(props, caller))
}
