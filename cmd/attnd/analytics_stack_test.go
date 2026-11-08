package main_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEachAttnCommandWritesOneCLIRecordNamedByItsPath(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	claude := s.Launched(session)
	fromAgent := func(args ...string) testworld.Result {
		t.Helper()
		return s.Run(testworld.Invocation{Args: args, Session: session})
	}
	var seed struct {
		ID string `json:"id"`
	}
	fromAgent("seed", "plant", "Add a discount field", "--json").JSON(t, &seed)
	commands := func() {
		t.Helper()
		fromAgent("seed", "show", seed.ID)
		fromAgent("seed", "show", "s-zzzzzz")
		fromAgent("seed", "check", "report", seed.ID, "C1", "-m", "the field saves")
		fromAgent("seed", "ls", "--bogus")
		fromAgent("terminal", "read", "t-missing")
		s.Attn("frobnicate")
		s.Attn("codex", "/private/project-sentinel")
		s.Attn("delegate", "roles", "--json")
	}

	commands()
	if got := cliRecords(t, app); len(got) != 0 {
		t.Fatalf("with analytics off the outbox holds cli records %v", got)
	}

	if set := s.Attn("settings", "set", "analytics.enabled", "true"); set.Code != 0 {
		t.Fatalf("enable analytics: exit %d, stderr %q", set.Code, set.Stderr)
	}
	commands()
	app.TypeLine(session, "add a discount field")
	if got := claude.Prompted(); got != "add a discount field" {
		t.Fatalf("claude received %q", got)
	}
	claude.Reply("Added.")
	if closed := s.Run(testworld.Invocation{Args: []string{"agent", "close", "--self", "-m", "done"}, Terminal: app.Terminal(session)}); closed.Code != 0 {
		t.Fatalf("self-close: %+v", closed)
	}

	var got []string
	hooks := 0
	subjects := map[string]string{}
	for _, record := range cliRecords(t, app) {
		line := record.Name + " " + string(record.Props)
		if strings.HasPrefix(record.Name, "_hook-") {
			hooks++
			subjects["_hook-"] = record.Subject
			if !strings.Contains(line, `"caller":"agent","exit_code":0,"harness":"claude","ok":true`) || record.Subject == "" {
				t.Errorf("hook record %s with subject %q, want it ok from the claude session", line, record.Subject)
			}
			continue
		}
		subjects[record.Name] = record.Subject
		got = append(got, line)
	}
	slices.Reverse(got)
	want := []string{
		`settings set {"caller":"human","exit_code":0,"ok":true}`,
		`seed show {"caller":"agent","exit_code":0,"harness":"claude","ok":true}`,
		`seed show {"caller":"agent","error_kind":"refused","exit_code":1,"harness":"claude","ok":false,"refusal":"not_found"}`,
		`seed check report {"caller":"agent","error_kind":"refused","exit_code":1,"harness":"claude","ok":false,"refusal":"not_found"}`,
		`seed ls {"caller":"agent","error_kind":"unknown_flag","exit_code":2,"harness":"claude","ok":false}`,
		`terminal read {"caller":"agent","error_kind":"refused","exit_code":1,"harness":"claude","ok":false,"refusal":"unclassified"}`,
		`attn {"caller":"human","error_kind":"unknown_command","exit_code":1,"ok":false}`,
		`codex {"caller":"human","error_kind":"usage","exit_code":2,"ok":false}`,
		`delegate roles {"caller":"human","exit_code":0,"ok":true}`,
		`agent close {"caller":"agent","exit_code":0,"harness":"claude","ok":true}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("cli records, oldest first:\n%s\nwant one per command, no query:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if hooks == 0 {
		t.Fatal("the turn's hooks wrote no _hook- records")
	}
	if subjects["agent close"] != subjects["_hook-"] {
		t.Fatalf("the self-close record has subject %q, want its session's %q", subjects["agent close"], subjects["_hook-"])
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
		if !strings.HasPrefix(record.Name, "_hook-") {
			continue
		}
		hooks[record.Name]++
		if string(record.Props) != `{"caller":"agent","exit_code":0,"harness":"codex","ok":true}` || record.Subject == "" {
			t.Errorf("hook record %s %s with subject %q, want it from the codex agent's session", record.Name, record.Props, record.Subject)
		}
	}
	if len(hooks) == 0 {
		t.Fatalf("no _hook- records from the turn's hooks")
	}
	t.Logf("hook records %v", hooks)
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
