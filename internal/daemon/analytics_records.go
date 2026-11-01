package daemon

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) recordAnalytics(stream analytics.Stream, name, subject string, props any) {
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
	secret, err := ensureAnalyticsSecret(d.dataRoot)
	if err != nil {
		return err
	}
	record := analytics.Record{
		Stream:  stream,
		Name:    name,
		Subject: analytics.HashSubject(secret, subject),
		At:      time.Now().UTC(),
		Props:   analytics.Redact(encoded, analyticsFields(stream, name), secret),
	}
	if err := d.store.AppendAnalyticsStreamRecord(record, d.analyticsBuild()); err != nil {
		return fmt.Errorf("record %s/%s: %w", stream, name, err)
	}
	return nil
}

func analyticsFields(stream analytics.Stream, name string) analytics.Fields {
	switch stream {
	case analytics.StreamApp:
		return analytics.AppFields[name]
	case analytics.StreamCLI:
		return analytics.CLIFields
	case analytics.StreamSample:
		return analytics.SampleFields[name]
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
		if caller == "" {
			caller = protocol.Deref(msg.SessionID)
		}
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
	} else if closed := d.store.SessionLedgerEntry(sessionID); closed != nil {
		props["harness"] = closed.Agent
	}
	return props, sessionID
}

func socketFrameCaller(frame []byte) (cmd string, caller protocol.TerminalID) {
	var fields struct {
		Cmd string `json:"cmd"`
		protocol.SocketCaller
	}
	_ = json.Unmarshal(frame, &fields)
	return fields.Cmd, protocol.Deref(fields.CallerID)
}
