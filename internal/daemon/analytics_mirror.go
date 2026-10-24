package daemon

import (
	"context"
	"strings"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/bus"
)

const (
	analyticsMirrorConsumer = "analytics-mirror"
	ventFactPrefix          = "vent."
)

func (d *Daemon) startAnalyticsMirror(secret []byte) error {
	if d.eventBus.Registered(analyticsMirrorConsumer) {
		return nil
	}
	return d.eventBus.Register(analyticsMirrorConsumer, bus.All, func(_ context.Context, ev bus.Event) error {
		if strings.HasPrefix(ev.Name, ventFactPrefix) {
			return nil
		}
		return d.store.AppendAnalyticsRecord(analytics.Record{
			Stream:  analytics.StreamBus,
			Seq:     ev.Seq,
			Name:    ev.Name,
			Subject: analytics.HashSubject(secret, ev.Subject),
			At:      ev.CreatedAt,
			Bytes:   len(ev.Payload),
			Props:   analytics.Redact(ev.Payload, analytics.SafeValues[ev.Name]),
		})
	})
}

func (d *Daemon) stopAnalyticsMirror() error {
	return d.eventBus.Unregister(analyticsMirrorConsumer)
}
