package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/appbuild"
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
		Name    string `json:"name"`
		Cursor  int    `json:"cursor"`
		Lag     int    `json:"lag"`
		Enabled bool   `json:"enabled"`
	} `json:"consumers"`
}

func readBusLog(t *testing.T, s *testworld.Stack) busLog {
	t.Helper()
	var log busLog
	s.Attn("bus", "status", "--json").JSON(t, &log)
	return log
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
	declaration := fmt.Sprintf(`{"name":%q,"attn_app_api":1,"entrypoint":"src/index.ts","subscribe":[{"events":["document.changed"]}]}`, name)
	bundle := []byte("export default {}")
	hash := appbuild.VersionHash(declaration, bundle, nil)
	path := appbuild.ArtifactPath(filepath.Join(s.Dir, "apps"), name, hash)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bundle, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.AppApply(name, hash, declaration, ""); err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
}

func TestTheBusCommandsReportAndTrimTheLogTheDaemonWrote(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	if empty := readBusLog(t, s); empty.Rows != 0 || len(empty.Producers) != 0 {
		t.Fatalf("bus status over a fresh database = %+v, want an empty log", empty)
	}

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
	if table := s.Attn("bus", "status").Stdout; !strings.Contains(table, fmt.Sprintf("log: seq %d..%d, %d event(s)", log.Earliest, log.Head, log.Rows)) {
		t.Errorf("bus status printed:\n%s", table)
	}

	if got, want := trimBus(t, s), fmt.Sprintf("removed 0 event(s); log now holds %d of %d, weighing", log.Rows, log.Rows); !strings.HasPrefix(got, want) {
		t.Errorf("a trim with every document change unread by the enabled history app printed %q, want %q...", got, want)
	}

	s.Start()
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
	for _, p := range after.Producers {
		if p.Name == "document.changed" && p.Events != 2 {
			t.Errorf("with no app left to read them, a trim kept %d document changes, want the newest of each of the two documents", p.Events)
		}
	}
}
