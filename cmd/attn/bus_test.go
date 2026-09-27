package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bus"
)

var busRenderNow = time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

func renderBusStatus(s bus.Status) string {
	var buf bytes.Buffer
	writeBusStatus(&buf, s, busRenderNow)
	return buf.String()
}

func busStatusFixture(producers ...bus.Producer) bus.Status {
	s := bus.Status{
		Earliest:        1,
		Head:            1000,
		Rows:            1000,
		Bytes:           64_000,
		OldestAt:        busRenderNow.Add(-8 * 24 * time.Hour),
		NewestAt:        busRenderNow,
		RetentionWindow: 30 * 24 * time.Hour,
		Producers:       producers,
	}
	return s
}

func TestBusStatusRenderingMarksALoudProducerAndPrintsTheFindings(t *testing.T) {
	s := busStatusFixture(
		bus.Producer{
			Name: "session.state.changed", Events: 900, Share: 0.9, Subjects: 4,
			RecentPerHour: 1800, BaselinePerHour: 1892, SustainedPerHour: 680,
			Surging: true, SurgeWindow: bus.BaselineWindow, SurgePerHour: 1892,
		},
		bus.Producer{Name: "pr.updated", Events: 100, Share: 0.1, Subjects: 50},
	)
	s.Health = []bus.Health{
		{Level: bus.HealthError, Kind: bus.HealthConsumerLagging, Subject: "notifier",
			Message: "consumer notifier is 41,141 events behind and not advancing"},
		{Level: bus.HealthWarn, Kind: bus.HealthProducerSurging, Subject: "session.state.changed",
			Message: "producer session.state.changed is publishing 1892 events/hour"},
	}
	out := renderBusStatus(s)

	loud := producerRow(t, out, "session.state.changed")
	if !strings.Contains(loud, "!") {
		t.Errorf("a loud producer is not marked in the table: %q", loud)
	}
	if quiet := producerRow(t, out, "pr.updated"); strings.Contains(quiet, "!") {
		t.Errorf("a quiet producer must not be marked: %q", quiet)
	}
	if !strings.Contains(out, "ERROR: consumer notifier is 41,141 events behind") {
		t.Errorf("the lagging finding is missing:\n%s", out)
	}
	if !strings.Contains(out, "WARN: producer session.state.changed is publishing") {
		t.Errorf("the surging finding is missing:\n%s", out)
	}
}

func TestBusStatusJSONCarriesBothSustainedWindows(t *testing.T) {
	report := busStatusReport(busStatusFixture(bus.Producer{
		Name: "session.state.changed", Events: 1000, Share: 1, Subjects: 4,
		RecentPerHour: 1800, BaselinePerHour: 1892, SustainedPerHour: 680,
		Surging: true, SurgeWindow: bus.BaselineWindow, SurgePerHour: 1892,
	}))
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Producers []struct {
			SustainedPerHour   float64 `json:"sustained_per_hour"`
			SurgePerHour       float64 `json:"surge_per_hour"`
			SurgeWindowSeconds float64 `json:"surge_window_seconds"`
			Surging            bool    `json:"surging"`
		} `json:"producers"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Producers) != 1 {
		t.Fatalf("producers = %d, want 1", len(decoded.Producers))
	}
	p := decoded.Producers[0]
	if p.SustainedPerHour != 680 {
		t.Errorf("sustained_per_hour = %v, want the 6h rate 680", p.SustainedPerHour)
	}
	if p.SurgeWindowSeconds != bus.BaselineWindow.Seconds() {
		t.Errorf("surge_window_seconds = %v, want the 24h window that tripped", p.SurgeWindowSeconds)
	}
	if p.SurgePerHour != 1892 || !p.Surging {
		t.Errorf("surge = %v/h surging=%v, want 1892 and true", p.SurgePerHour, p.Surging)
	}
}

func producerRow(t *testing.T, out, name string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), name) {
			return line
		}
	}
	t.Fatalf("no table row for %q in:\n%s", name, out)
	return ""
}
