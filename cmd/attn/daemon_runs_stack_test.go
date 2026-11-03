package main_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestDaemonStartRecordsHowThePreviousRunEnded(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	first := testworld.AttnUpdateBinary(t, "runs-before", "stable", "")
	next := testworld.AttnUpdateBinary(t, "runs-after", "stable", "")

	s.StartBinary(first)
	app := s.App()
	enableAnalytics(app)
	sampleOnce(s, app)
	app.Close()

	if got := s.Attn("daemon", "stop"); got.Code != 0 {
		t.Fatalf("attn daemon stop: %+v", got)
	}
	s.AwaitStopped()
	s.StartBinary(first)
	sampleOnce(s, s.App())

	s.Kill()
	s.StartBinary(first)
	sampleOnce(s, s.App())

	t.Cleanup(func() { s.Run(testworld.Invocation{Binary: next, Args: []string{"daemon", "stop"}}) })
	got := s.Run(testworld.Invocation{Binary: next, Args: []string{"daemon", "ensure"}})
	var ensured struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	got.JSON(t, &ensured)
	if got.Code != 0 || ensured.Status != "restarted" || ensured.Reason != "source_fingerprint_mismatch" {
		t.Fatalf("attn daemon ensure from a second build: %+v", got)
	}
	s.AwaitStopped()
	if got := s.Run(testworld.Invocation{Binary: next, Args: []string{"daemon", "ensure"}}); got.Code != 0 || !strings.Contains(got.Stdout, `"already_running"`) {
		t.Fatalf("attn daemon ensure from the replacement build: %+v", got)
	}
	records := sampleRecords(t, s.App())

	var runs []string
	var starts []analytics.Record
	samples := map[string]int{}
	for _, record := range records {
		if record.Subject == "" {
			t.Errorf("%s record has no subject: %s", record.Name, record.Props)
		}
		switch record.Name {
		case "daemon_start":
			starts = append(starts, record)
			runs = append(runs, record.Subject)
		case "process":
			if len(runs) == 0 || runs[len(runs)-1] != record.Subject {
				runs = append(runs, record.Subject)
			}
			samples[record.Subject]++
		}
	}
	want := []string{
		`{"previous_end":"stopped","stop_reason":"stop"}`,
		`{"previous_end":"exited_unexpectedly"}`,
		`{"previous_end":"stopped","stop_reason":"source_fingerprint_mismatch"}`,
	}
	if len(starts) != len(want) {
		t.Fatalf("daemon_start records %v, want %d", starts, len(want))
	}
	for i, start := range starts {
		var props map[string]any
		if err := json.Unmarshal(start.Props, &props); err != nil {
			t.Fatal(err)
		}
		previous, _ := props["previous_run"].(string)
		delete(props, "previous_run")
		got, _ := json.Marshal(props)
		if string(got) != want[i] {
			t.Errorf("daemon_start %d props %s, want %s plus previous_run", i+1, got, want[i])
		}
		prior := startIndex(runs, start.Subject) - 1
		if prior < 0 || runs[prior] != previous {
			t.Errorf("daemon_start %d previous_run %q, want the subject of the run before it in %v", i+1, previous, runs)
		}
	}
	if len(runs) != 4 {
		t.Fatalf("records name runs %v, want 4 distinct runs in order", runs)
	}
	for _, run := range runs[:3] {
		if samples[run] != 1 {
			t.Errorf("run %s has %d process samples, want 1", run, samples[run])
		}
	}
	t.Logf("runs %v; starts %v", runs, starts)
}

func startIndex(runs []string, subject string) int {
	for i, run := range runs {
		if run == subject {
			return i
		}
	}
	return -1
}

func enableAnalytics(app *testworld.Peer) {
	app.T.Helper()
	requestID := uuid.NewString()
	answer := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "analytics.enabled", Value: "true", RequestID: protocol.Ptr(requestID)},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
	if !protocol.Deref(answer.Success) {
		app.T.Fatalf("setting analytics.enabled refused: %s", protocol.Deref(answer.Error))
	}
}

func sampleOnce(s *testworld.Stack, app *testworld.Peer) {
	s.T.Helper()
	defer app.Close()
	s.Advance(5*time.Minute + time.Second)
	s.AwaitJobsSettled()
}

func sampleRecords(t *testing.T, app *testworld.Peer) []analytics.Record {
	t.Helper()
	defer app.Close()
	requestID := uuid.NewString()
	listed := testworld.Request(app, protocol.ListAnalyticsRecordsMessage{Cmd: protocol.CmdListAnalyticsRecords, RequestID: protocol.Ptr(requestID), Limit: 1000, Stream: protocol.Ptr(string(analytics.StreamSample))},
		protocol.EventListAnalyticsRecordsResult, func(m protocol.ListAnalyticsRecordsResultMessage) bool { return m.RequestID == requestID })
	if !listed.Success {
		t.Fatalf("list analytics records: %s", protocol.Deref(listed.Error))
	}
	records := make([]analytics.Record, 0, len(listed.Records))
	for i := len(listed.Records) - 1; i >= 0; i-- {
		var record analytics.Record
		if err := json.Unmarshal([]byte(listed.Records[i].Record), &record); err != nil {
			t.Fatalf("outbox row %d: %v", listed.Records[i].ID, err)
		}
		records = append(records, record)
	}
	return records
}
