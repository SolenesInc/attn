package main_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestCLIOutcomesReachTheOutboxOnlyWhileAnalyticsIsOn(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	s.Launched(session)
	run := func() {
		t.Helper()
		s.Attn("list")
		s.Run(testworld.Invocation{Args: []string{"list"}, Session: session})
		s.Attn("seed", "show", "s-zzzzzz")
		s.Attn("seed", "frobnicate")
		s.Attn("seed", "ls", "--bogus")
		s.Attn("frobnicate")
	}

	run()
	if got := cliRecords(t, app); len(got) != 0 {
		t.Fatalf("with analytics off the outbox holds cli records %v", got)
	}

	if set := s.Attn("settings", "set", "analytics.enabled", "true"); set.Code != 0 {
		t.Fatalf("enable analytics: exit %d, stderr %q", set.Code, set.Stderr)
	}
	run()
	recorded := map[string]bool{}
	for _, record := range cliRecords(t, app) {
		recorded[record.Name+" "+string(record.Props)] = true
	}
	for _, want := range []string{
		`query {"caller":"human","ok":true}`,
		`query {"caller":"agent","harness":"claude","ok":true}`,
		`seed_show {"caller":"human","error_kind":"refused","ok":false}`,
		`seed {"caller":"human","error_kind":"unknown_command","ok":false}`,
		`seed ls {"caller":"human","error_kind":"unknown_flag","ok":false}`,
		`attn {"caller":"human","error_kind":"unknown_command","ok":false}`,
	} {
		if !recorded[want] {
			t.Errorf("no cli record %s; recorded %v", want, recorded)
		}
	}
}

func cliRecords(t *testing.T, app *testworld.Peer) []analytics.Record {
	t.Helper()
	id := uuid.NewString()
	listed := testworld.Request(app, protocol.ListAnalyticsRecordsMessage{Cmd: protocol.CmdListAnalyticsRecords, RequestID: protocol.Ptr(id), Limit: 100, Stream: protocol.Ptr("cli")},
		protocol.EventListAnalyticsRecordsResult, func(r protocol.ListAnalyticsRecordsResultMessage) bool { return r.RequestID == id })
	var records []analytics.Record
	for _, row := range listed.Records {
		var record analytics.Record
		if err := json.Unmarshal([]byte(row.Record), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}
