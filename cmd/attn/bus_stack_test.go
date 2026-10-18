package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type busLog struct {
	Earliest  int    `json:"earliest"`
	Head      int    `json:"head"`
	Rows      int    `json:"rows"`
	Bytes     int    `json:"bytes"`
	OldestAt  string `json:"oldest_at"`
	NewestAt  string `json:"newest_at"`
	Producers []struct {
		Name            string  `json:"name"`
		Events          int     `json:"events"`
		Bytes           int     `json:"bytes"`
		Subjects        int     `json:"subjects"`
		RecentPerHour   float64 `json:"recent_per_hour"`
		BaselinePerHour float64 `json:"baseline_per_hour"`
	} `json:"producers"`
	Consumers []struct {
		Name                string `json:"name"`
		Cursor              int    `json:"cursor"`
		Lag                 int    `json:"lag"`
		Enabled             bool   `json:"enabled"`
		HoldsRetentionFloor bool   `json:"holds_retention_floor"`
		PinAlarm            bool   `json:"pin_alarm"`
		PinnedBytes         int    `json:"pinned_bytes"`
	} `json:"consumers"`
	PinAlarmSeconds float64 `json:"pin_alarm_seconds"`
}

func readBusLog(t *testing.T, s *testworld.Stack, env ...string) busLog {
	t.Helper()
	var log busLog
	s.Run(testworld.Invocation{Args: []string{"bus", "status", "--json"}, Env: env}).JSON(t, &log)
	return log
}

func busTable(s *testworld.Stack, env ...string) string {
	s.T.Helper()
	return s.Run(testworld.Invocation{Args: []string{"bus", "status"}, Env: env}).Stdout
}

func assertBusTable(t *testing.T, table string, present, absent []string) {
	t.Helper()
	for _, want := range present {
		if !strings.Contains(table, want) {
			t.Errorf("bus status does not show %q:\n%s", want, table)
		}
	}
	for _, unwanted := range absent {
		if strings.Contains(table, unwanted) {
			t.Errorf("bus status shows %q:\n%s", unwanted, table)
		}
	}
}

func trimBus(t *testing.T, s *testworld.Stack) string {
	t.Helper()
	trimmed := s.Attn("bus", "trim")
	if trimmed.Code != 0 {
		t.Fatalf("attn bus trim exited %d: %s", trimmed.Code, trimmed.Stderr)
	}
	return strings.TrimSpace(trimmed.Stdout)
}

func TestTheBusCommandsReportAndTrimTheLogTheDaemonWrote(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	if before := s.Attn("bus", "status"); before.Code == 0 || !strings.Contains(before.Stderr, "attn.db") {
		t.Fatalf("bus status before any daemon created the database exited %d: %s%s, want a refusal naming it", before.Code, before.Stdout, before.Stderr)
	}

	s.Start()
	assertBusTable(t, busTable(s), []string{"log: seq "}, []string{"ERROR", "WARN"})
	if r := s.Attn("bus", "disable", "garden-seed-bells"); r.Code != 0 {
		t.Fatalf("attn bus disable garden-seed-bells exited %d: %s", r.Code, r.Stderr)
	}
	documentEvents := func(log busLog) int {
		for _, producer := range log.Producers {
			if producer.Name == "document.changed" {
				return producer.Events
			}
		}
		return 0
	}
	setupEvents := documentEvents(readBusLog(t, s))
	cli := s.Client()
	if _, err := cli.DocDefine(protocol.DocumentCollectionSchema{Namespace: "test/history", Collection: "requests"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if _, err := cli.DocPut("test/history", "requests", id, `{}`, nil); err != nil {
			t.Fatal(err)
		}
	}
	s.Stop()

	before := readBusLog(t, s)
	if before.Rows == 0 || before.Bytes == 0 || before.OldestAt == "" || before.OldestAt > before.NewestAt {
		t.Fatalf("bus status after document writes = %+v", before)
	}
	if changed := documentEvents(before) - setupEvents; changed != 2 {
		t.Errorf("document.changed events = %d, want two writes", changed)
	}
	foundDisabled := false
	for _, consumer := range before.Consumers {
		if consumer.Name == "garden-seed-bells" {
			foundDisabled = !consumer.Enabled && !consumer.HoldsRetentionFloor
		}
	}
	if !foundDisabled {
		t.Errorf("garden-seed-bells is not disabled without a retention pin: %+v", before.Consumers)
	}
	assertBusTable(t, busTable(s), []string{"WARN: consumer garden-seed-bells is disabled"}, nil)

	if result := trimBus(t, s); !strings.HasPrefix(result, "removed ") {
		t.Errorf("bus trim output = %q", result)
	}
	if after := readBusLog(t, s); after.Rows > before.Rows {
		t.Errorf("bus trim grew the log from %d to %d rows", before.Rows, after.Rows)
	}
	if r := s.Attn("bus", "enable", "garden-seed-bells"); r.Code != 0 {
		t.Fatalf("attn bus enable garden-seed-bells exited %d: %s", r.Code, r.Stderr)
	}
	for _, consumer := range readBusLog(t, s).Consumers {
		if consumer.Name == "garden-seed-bells" && !consumer.Enabled {
			t.Error("garden-seed-bells remained disabled after bus enable")
		}
	}
}
