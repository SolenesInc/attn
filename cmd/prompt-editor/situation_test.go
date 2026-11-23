package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/prompts"
)

func copyCommitted(t *testing.T, e *editor, dir string) {
	t.Helper()
	names, err := filepath.Glob(filepath.Join("../../internal/prompts", dir, "*.json"))
	if err != nil || len(names) == 0 {
		t.Fatalf("committed %s: %v", dir, err)
	}
	if err := e.root.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.root.WriteFile(dir+"/"+filepath.Base(name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func page(t *testing.T, e *editor, q pageRequest) pageResponse {
	t.Helper()
	w := request(t, e, "page", q)
	if w.Code != 200 {
		t.Fatalf("page: %d %s", w.Code, w.Body)
	}
	var response pageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestPageShowsACrewMemberStartWithPrimingNestedInLaunch(t *testing.T) {
	e := testEditor(t)
	copyCommitted(t, e, "scenarios")
	copyCommitted(t, e, "situations")
	response := page(t, e, pageRequest{Situation: "crew-member-start"})
	var order []string
	for _, event := range response.Events {
		order = append(order, event.Event)
	}
	if strings.Join(order, " ") != "session/launch session/garden-ready crew/new-session" {
		t.Fatalf("events out of order: %v", order)
	}
	launch := response.Events[0]
	priming, ok := launch.Producers["crew_priming"]
	if !ok || priming.Event != "crew/priming" || priming.Scenario != "crew-v2-with-handoff" || priming.Trace.Text == "" {
		t.Fatalf("crew_priming is not nested under launch: %+v", launch.Producers)
	}
	if !strings.Contains(launch.Trace.Text, strings.TrimSpace(priming.Trace.Text)) {
		t.Fatal("launch does not deliver the nested priming text")
	}
	if _, ok := priming.Producers["garden_holdings"]; !ok {
		t.Fatal("priming's own producer is not nested under it")
	}
	charter := "content/crew/v2/charter.md"
	if _, ok := response.Sources[charter]; !ok {
		t.Fatalf("page sources lack the priming's %s", charter)
	}
	for path, source := range response.Sources {
		if source.Revision == "" {
			t.Fatalf("%s has no revision", path)
		}
		for _, use := range source.Uses {
			if use.Event == "session/launch" || use.Event == "crew/priming" {
				t.Fatalf("%s lists %s, which is on this page", path, use.Event)
			}
		}
	}

	flipped := page(t, e, pageRequest{Situation: "crew-member-start", Flips: map[string]string{"has_charter": "false"}})
	if flipped.Events[0].Producers["crew_priming"].Values["has_charter"] != "false" || flipped.Events[0].Trace.Text == launch.Trace.Text {
		t.Fatal("a flip did not reach the nested producer")
	}
	if disk, _ := e.root.ReadFile("scenarios/crew-v2-with-handoff.json"); !strings.Contains(string(disk), `"has_charter": "true"`) {
		t.Fatal("a flip wrote the scenario file")
	}

	single := page(t, e, pageRequest{Event: "delegation/opening"})
	if len(single.Events) != 1 || single.Events[0].Scenario != "" || single.Events[0].Values["seed_id"] != "" {
		t.Fatalf("bare event page: %+v", single.Events)
	}
}

func TestCheckRefusesASituationWithAMissingScenario(t *testing.T) {
	e := testEditor(t)
	copyCommitted(t, e, "scenarios")
	copyCommitted(t, e, "situations")
	if code, out := runTestCLI(t, e, "", "check"); code != 0 {
		t.Fatalf("committed situations fail check: %d %s", code, out)
	}
	if err := e.root.WriteFile("situations/broken.json", []byte(`{"version":1,"id":"broken","description":"","scenarios":["crew-launch","no-such-scenario"]}`), 0644); err != nil {
		t.Fatal(err)
	}
	code, out := runTestCLI(t, e, "", "check")
	if code != 1 || !strings.Contains(out, "no-such-scenario") {
		t.Fatalf("check passed a missing scenario: %d %s", code, out)
	}
	if err := e.root.WriteFile("situations/renamed.json", []byte(`{"version":1,"id":"other","description":"","scenarios":["crew-launch"]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if code, out := runTestCLI(t, e, "", "check"); code != 2 || !strings.Contains(out, "match filename") {
		t.Fatalf("check passed an id that is not its filename: %d %s", code, out)
	}
}

func TestPageWarnsWhenGoDefinitionsChangedSinceTheCatalog(t *testing.T) {
	e := testEditor(t)
	copyCommitted(t, e, "scenarios")
	copyCommitted(t, e, "situations")
	snapshot, err := e.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	m, err := prompts.ParseManifest(snapshot.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if m.DefinitionsHash, err = prompts.DefinitionsHash(e.root.FS()); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(m)
	writeTest(t, e, prompts.ManifestPath, string(data))
	if fresh := page(t, e, pageRequest{Situation: "crew-member-start"}); fresh.Stale != "" {
		t.Fatalf("a current catalog reads as stale: %s", fresh.Stale)
	}
	writeTest(t, e, "new.go", "package prompts\n")
	stale := page(t, e, pageRequest{Situation: "crew-member-start"})
	if !strings.Contains(stale.Stale, "refresh") || len(stale.Events) != 3 {
		t.Fatalf("stale catalog: %q with %d events", stale.Stale, len(stale.Events))
	}
}
