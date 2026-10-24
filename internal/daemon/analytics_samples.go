package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/diag"
	"github.com/victorarias/attn/internal/jobs"
)

const (
	analyticsSampleKind     = "analytics_sample"
	analyticsSampleInterval = 5 * time.Minute
	analyticsSampleTimeout  = time.Minute
)

type analyticsSample struct {
	diag.ProcessSample
	DatabaseBytes    int64 `json:"database_bytes"`
	BusRetainedFacts int64 `json:"bus_retained_facts"`
	ConnectedClients int   `json:"connected_clients"`
}

func (d *Daemon) analyticsSampleHandler(_ context.Context, _ *jobs.Job) (any, error) {
	if !d.analyticsEnabled() {
		return nil, nil
	}
	sample := analyticsSample{ProcessSample: diag.Sample(), ConnectedClients: d.wsHub.ClientCount()}
	var err error
	if sample.DatabaseBytes, err = d.store.DatabaseBytes(); err != nil {
		return nil, fmt.Errorf("sampling the database size: %w", err)
	}
	earliest, head, err := d.store.BusBounds()
	if err != nil {
		return nil, fmt.Errorf("sampling the bus size: %w", err)
	}
	if head > 0 {
		sample.BusRetainedFacts = head - earliest + 1
	}
	return nil, d.appendAnalytics(analytics.StreamSample, "process", sample)
}
