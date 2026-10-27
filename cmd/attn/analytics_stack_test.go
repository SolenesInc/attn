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

func TestCLIUsageErrorsReachTheOutboxOnlyWhileAnalyticsIsOn(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	s.Launched(session)
	run := func() {
		t.Helper()
		s.Run(testworld.Invocation{Args: []string{"seed", "frobnicate"}, Session: session})
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
		`seed {"caller":"agent","error_kind":"unknown_command","harness":"claude","ok":false}`,
		`seed ls {"caller":"human","error_kind":"unknown_flag","ok":false}`,
		`attn {"caller":"human","error_kind":"unknown_command","ok":false}`,
	} {
		if !recorded[want] {
			t.Errorf("no cli record %s; recorded %v", want, recorded)
		}
	}
}

func TestASharedCodexHookIsRecordedAsItsAgentNotAPerson(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	shareCodex(t, app)
	if set := s.Attn("settings", "set", "analytics.enabled", "true"); set.Code != 0 {
		t.Fatalf("enable analytics: exit %d, stderr %q", set.Code, set.Stderr)
	}
	session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	codex := s.Launched(session)
	app.TypeLine(session, "add a discount field")
	if got := codex.Prompted(); got != "add a discount field" {
		t.Fatalf("codex received %q", got)
	}
	codex.Reply("Added. <!-- attn:state=idle -->")

	hooks := map[string]int{}
	for _, record := range cliRecords(t, app) {
		if record.Name != "state" && record.Name != "set_session_resume_id" && record.Name != "stop" {
			continue
		}
		hooks[record.Name]++
		if string(record.Props) != `{"caller":"agent","harness":"codex","ok":true}` || record.Subject == "" {
			t.Errorf("hook record %s %s with subject %q, want it from the codex agent's session", record.Name, record.Props, record.Subject)
		}
	}
	if hooks["state"] == 0 || hooks["set_session_resume_id"] == 0 || hooks["stop"] == 0 {
		t.Fatalf("hook records %v, want state, set_session_resume_id and stop from the turn's hooks", hooks)
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
