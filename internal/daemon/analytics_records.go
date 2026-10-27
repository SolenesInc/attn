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

func (d *Daemon) recordAnalytics(stream analytics.Stream, name, subject string, props map[string]any) {
	if !d.analyticsEnabled() {
		return
	}
	if err := d.appendAnalytics(stream, name, subject, props); err != nil {
		d.logf("analytics: %v", err)
	}
}

func (d *Daemon) appendAnalytics(stream analytics.Stream, name, subject string, props any) error {
	encoded, err := json.Marshal(props)
	if err != nil {
		return fmt.Errorf("encode %s/%s props: %w", stream, name, err)
	}
	record := analytics.Record{Stream: stream, Name: name, At: time.Now().UTC(), Props: analytics.Redact(encoded, analyticsSafeValues(stream, name))}
	if subject != "" {
		secret, err := ensureAnalyticsSecret(d.dataRoot)
		if err != nil {
			return err
		}
		record.Subject = analytics.HashSubject(secret, subject)
	}
	if err := d.store.AppendAnalyticsStreamRecord(record); err != nil {
		return fmt.Errorf("record %s/%s: %w", stream, name, err)
	}
	return nil
}

func analyticsSafeValues(stream analytics.Stream, name string) analytics.Allowlist {
	switch stream {
	case analytics.StreamApp:
		return analytics.AppSafeValues[name]
	case analytics.StreamCLI:
		return analytics.CLISafeValues
	default:
		return nil
	}
}

func (d *Daemon) handleRecordAnalytics(msg *protocol.RecordAnalyticsMessage, caller protocol.SessionID) {
	stream := analytics.Stream(msg.Stream)
	if stream != analytics.StreamApp && stream != analytics.StreamCLI {
		d.logf("analytics: record_analytics refused stream %q: use app or cli", msg.Stream)
		return
	}
	props := map[string]any(msg.Props)
	var sessionID protocol.SessionID
	if stream == analytics.StreamCLI {
		props, sessionID = d.withAnalyticsCaller(props, caller)
	}
	d.recordAnalytics(stream, msg.Name, string(sessionID), props)
}

func (d *Daemon) withAnalyticsCaller(props map[string]any, sessionID protocol.SessionID) (map[string]any, protocol.SessionID) {
	if props == nil {
		props = map[string]any{}
	}
	props["caller"] = "human"
	if sessionID == "" {
		return props, ""
	}
	props["caller"] = "agent"
	if session := d.store.Get(sessionID); session != nil {
		props["harness"] = session.Agent
	}
	return props, sessionID
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
	return fields.Cmd, protocol.Deref(fields.CallerID)
}

func (d *Daemon) recordingCLICommand(conn net.Conn, frame []byte, parseErr error, caller protocol.SessionID) (*analyticsReplyConn, func()) {
	cmd, _ := socketFrameCaller(frame)
	props, sessionID := d.withAnalyticsCaller(nil, caller)
	record := func(errorKind string) {
		props["ok"] = errorKind == ""
		if errorKind != "" {
			props["error_kind"] = errorKind
		}
		d.recordAnalytics(analytics.StreamCLI, cmd, string(sessionID), props)
	}
	reply := &analyticsReplyConn{Conn: conn}
	reply.answered = func(failed bool) {
		switch {
		case parseErr != nil:
			record(cliErrorUnknownCommand)
		case failed:
			record(cliErrorRefused)
		default:
			record("")
		}
	}
	unanswered := func() {
		if !reply.replied {
			record(cliErrorNoReply)
		}
	}
	return reply, unanswered
}
