package main_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
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

func installSubscribedApp(t *testing.T, s *testworld.Stack, cli *client.Client, name string) {
	t.Helper()
	applyAppDeclaration(t, s, cli, name, fmt.Sprintf(`{"name":%q,"attn_app_api":1,"entrypoint":"src/index.ts","subscribe":[{"events":["document.changed"]}]}`, name), "export default {}")
}

func TestTheBusCommandsReportAndTrimTheLogTheDaemonWrote(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	if empty := readBusLog(t, s); empty.Rows != 0 || len(empty.Producers) != 0 {
		t.Fatalf("bus status over a fresh database = %+v, want an empty log", empty)
	}
	assertBusTable(t, busTable(s), []string{"log: seq ", "no registered consumers"}, []string{"producers", "ERROR", "WARN"})

	s.Start()
	cli := s.Client()
	if _, err := cli.DocDefine(protocol.DocumentCollectionSchema{Namespace: "app/history", Collection: "requests"}); err != nil {
		t.Fatal(err)
	}
	putRequest := func(id string) {
		if _, err := cli.DocPut("app/history", "requests", id, `{}`, nil); err != nil {
			t.Fatal(err)
		}
	}
	installSubscribedApp(t, s, cli, "ghost")
	installSubscribedApp(t, s, cli, "history")
	putRequest("a")
	installSubscribedApp(t, s, cli, "archive")
	if _, err := cli.AppSetEnabled("archive", false); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.AppRemove("ghost"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "a", "b"} {
		putRequest(id)
	}
	s.Stop()

	log := readBusLog(t, s)
	events, bytes := 0, 0
	for i, p := range log.Producers {
		events, bytes = events+p.Events, bytes+p.Bytes
		if i > 0 && p.Events > log.Producers[i-1].Events {
			t.Errorf("producer %s (%d events) is ranked below the quieter %s", p.Name, p.Events, log.Producers[i-1].Name)
		}
		if p.Name == "document.changed" && (p.Events != 4 || p.Subjects != 2 || p.RecentPerHour != 4 || p.BaselinePerHour != 4.0/24) {
			t.Errorf("document.changed = %+v, want 4 events over 2 subjects, all inside the last hour", p)
		}
	}
	if events != log.Rows || bytes != log.Bytes || log.OldestAt == "" || log.OldestAt > log.NewestAt {
		t.Errorf("the producers hold %d events of %d B, the log reports %d of %d B from %q to %q", events, bytes, log.Rows, log.Bytes, log.OldestAt, log.NewestAt)
	}
	consumers := map[string]bool{}
	for _, c := range log.Consumers {
		consumers[c.Name] = c.Enabled
	}
	if enabled, ok := consumers["app:history"]; !ok || !enabled {
		t.Errorf("consumers = %+v, want app:history enabled", log.Consumers)
	}
	if enabled, ok := consumers["app:archive"]; !ok || enabled {
		t.Errorf("consumers = %+v, want app:archive disabled", log.Consumers)
	}
	if _, ok := consumers["app:ghost"]; ok {
		t.Errorf("the removed app still has a consumer: %+v", log.Consumers)
	}
	assertBusTable(t, busTable(s),
		[]string{
			fmt.Sprintf("log: seq %d..%d, %d event(s)", log.Earliest, log.Head, log.Rows),
			"app:history (retention floor)",
			"WARN: consumer app:archive is disabled",
		},
		[]string{"quieter class", "PINNING"})

	tripwire := "ATTN_BUS_PIN_ALARM_AGE=1ms"
	pinned := readBusLog(t, s, tripwire)
	if pinned.PinAlarmSeconds != 0.001 {
		t.Errorf("pin_alarm_seconds = %v, want the 1ms tripwire", pinned.PinAlarmSeconds)
	}
	for _, c := range pinned.Consumers {
		if holds := c.Name == "app:history"; c.PinAlarm != holds || (c.PinnedBytes > 0) != holds || c.HoldsRetentionFloor != holds {
			t.Errorf("consumer %+v, want only app:history holding the floor and pinning its unread bytes", c)
		}
	}
	pinTable := busTable(s, tripwire)
	assertBusTable(t, pinTable, []string{"WARN: consumer app:history has pinned the retention floor"}, []string{"(retention floor)"})
	pinnedSize := regexp.MustCompile(`(?m)^app:history \(PINNING ([0-9.]+) (B|KB|MB)\)`).FindStringSubmatch(pinTable)
	if pinnedSize == nil || strings.Trim(pinnedSize[1], "0.") == "" {
		t.Errorf("bus status does not tag app:history with the size it pins:\n%s", pinTable)
	}

	if got, want := trimBus(t, s), fmt.Sprintf("removed 0 event(s); log now holds %d of %d, weighing", log.Rows, log.Rows); !strings.HasPrefix(got, want) {
		t.Errorf("a trim with every document change unread by the enabled history app printed %q, want %q...", got, want)
	}

	s.Start()
	busy := s.Client()
	if err := busy.Register("bus-session", "bus", s.Path("shop")); err != nil {
		t.Fatal(err)
	}
	if err := busy.RenameSession("bus-session", "renamed"); err != nil {
		t.Fatal(err)
	}
	if err := busy.UpdateTodos("bus-session", []string{"trim the log"}); err != nil {
		t.Fatal(err)
	}
	if err := busy.UpdateState("bus-session", "waiting_input"); err != nil {
		t.Fatal(err)
	}
	if err := busy.RecordPullRequestCreated("bus-session", "https://github.com/victorarias/attn/pull/1"); err != nil {
		t.Fatal(err)
	}
	if err := busy.RecordCompaction("bus-session", true, "auto"); err != nil {
		t.Fatal(err)
	}
	if _, err := busy.AppendJournal("bus-session", "", "trimmed the bus"); err != nil {
		t.Fatal(err)
	}
	if _, err := busy.CreateTicket("bus-session", "Trim the bus", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"history", "archive"} {
		if _, err := s.Client().AppRemove(name); err != nil {
			t.Fatal(err)
		}
	}
	s.Stop()
	before := readBusLog(t, s)
	compacted := trimBus(t, s)
	after := readBusLog(t, s)
	if want := fmt.Sprintf("removed %d event(s); log now holds %d of %d, weighing", before.Rows-after.Rows, after.Rows, before.Rows); !strings.HasPrefix(compacted, want) || after.Bytes >= before.Bytes {
		t.Errorf("a trim with no app left printed %q and weighed %d B, was %d B; want %q... and a lighter log", compacted, after.Bytes, before.Bytes, want)
	}
	if len(after.Producers) <= 15 {
		t.Fatalf("the log holds %d fact classes, too few to overflow the producer table", len(after.Producers))
	}
	hidden, hiddenEvents := after.Producers[15:], 0
	for _, p := range hidden {
		hiddenEvents += p.Events
	}
	table := busTable(s)
	assertBusTable(t, table, []string{fmt.Sprintf("... and %d quieter class(es) holding %d event(s), ", len(hidden), hiddenEvents), "(--json lists every one)"}, nil)
	for _, line := range strings.Split(table, "\n") {
		for _, p := range hidden {
			if strings.HasPrefix(line, p.Name+" ") {
				t.Errorf("the table shows %s, past its %d rows", p.Name, 15)
			}
		}
	}
	for _, p := range after.Producers {
		if p.Name == "document.changed" && p.Events != 2 {
			t.Errorf("with no app left to read them, a trim kept %d document changes, want the newest of each of the two documents", p.Events)
		}
	}
}
