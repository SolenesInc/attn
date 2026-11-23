package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/prompts"
)

func operate(t *testing.T, e *editor, q operationRequest) any {
	t.Helper()
	result, err := e.operation(t.Context(), q)
	if err != nil {
		t.Fatalf("%s: %v", q.Op, err)
	}
	return result
}
func expectConflict(t *testing.T, err error) {
	t.Helper()
	var detail *operationError
	if !errors.As(err, &detail) || detail.Code != "revision_conflict" {
		t.Fatalf("expected revision conflict, got %v", err)
	}
}
func runTestCLI(t *testing.T, e *editor, input string, args ...string) (int, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	code := runCLI(t.Context(), append([]string{"--repo", e.repo, "--json"}, args...), strings.NewReader(input), &out, &stderr)
	if stderr.Len() > 0 {
		t.Log(stderr.String())
	}
	return code, out.String()
}

func TestHeadlessDiscoveryUsesCheckoutDeclarations(t *testing.T) {
	e := testEditor(t)
	manifestTest(t, e, prompts.Recipient{ID: "custom", Events: []prompts.Event{prompts.On("launch", "user_message", "New checkout event", prompts.Input(prompts.TextField("brief", "Required input")))}})
	code, out := runTestCLI(t, e, "", "inspect", "custom/launch")
	if code != 0 || !strings.Contains(out, `"name": "brief"`) {
		t.Fatalf("inspect needs no values: %d %s", code, out)
	}
	code, out = runTestCLI(t, e, "", "inspect", "custom/launch", "--set", "brief=A checkout input")
	if code != 0 || !strings.Contains(out, "A checkout input") {
		t.Fatalf("render: %d %s", code, out)
	}
	code, out = runTestCLI(t, e, "", "list")
	if code != 0 || strings.Contains(out, "session/launch") {
		t.Fatalf("embedded definitions leaked: %d %s", code, out)
	}
	if err := e.root.Remove(prompts.ManifestPath); err != nil {
		t.Fatal(err)
	}
	code, out = runTestCLI(t, e, "", "list")
	if code != 2 || !strings.Contains(out, "refresh") {
		t.Fatalf("missing manifest must not fall back: %d %s", code, out)
	}
}

func TestScenarioProducerDiff(t *testing.T) {
	e := testEditor(t)
	gitTest(t, e, "init", "-b", "next")
	name := "content/producer.md"
	writeTest(t, e, name, "Before producer.")
	manifestTest(t, e, prompts.Recipient{ID: "example", Events: []prompts.Event{
		prompts.On("producer", "user_message", "", prompts.Use("producer", name)),
		prompts.On("consumer", "user_message", "", prompts.Input(prompts.ProducedBy(prompts.TextField("body", "Producer output"), "example/producer"))),
	}})
	for id, body := range map[string]string{
		"producer": `{"version":1,"id":"producer","recipient":"example","event":"producer","values":{}}`,
		"consumer": `{"version":1,"id":"consumer","recipient":"example","event":"consumer","values":{},"inputs":{"body":"producer"}}`,
	} {
		writeTest(t, e, "scenarios/"+id+".json", body)
	}
	base := commitTest(t, e)
	writeTest(t, e, name, "After producer.")
	result := operate(t, e, operationRequest{Op: "compare", Base: base, Mode: "tip", Scenario: "consumer"}).(map[string]any)
	c := result["scenarios"].([]scenarioCheck)[0]
	if !c.Changed || c.Base.Text != "Before producer." || c.Current.Text != "After producer." {
		t.Fatalf("producer hidden: %+v", c)
	}
	if _, ok := result["affected_events"].(map[string]string)["example/consumer"]; !ok {
		t.Fatal("missing indirect usage")
	}
	w := request(t, e, "page", pageRequest{Scenario: "consumer", Base: base})
	var page pageResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Base[name] != "Before producer." || page.Sources[name].Text != "After producer." {
		t.Fatalf("browser producer diff: %s", w.Body)
	}
}

func TestScenarioSaveAndDefinitionsRevision(t *testing.T) {
	e := testEditor(t)
	s := operate(t, e, operationRequest{Op: "scenario-save", ID: "wake", Event: "crew/new-session", Values: prompts.Values{}}).(scenario)
	_, err := e.operation(t.Context(), operationRequest{Op: "scenario-save", ID: s.ID, Event: "crew/new-session", Values: prompts.Values{}})
	expectConflict(t, err)
	operate(t, e, operationRequest{Op: "scenario-save", ID: s.ID, Event: "crew/new-session", Expect: s.Revision, Values: prompts.Values{}})
	snapshot, err := e.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	m, err := prompts.ParseManifest(snapshot.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	m.DefinitionsHash, err = prompts.DefinitionsHash(e.root.FS())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(m)
	writeTest(t, e, prompts.ManifestPath, string(data))
	writeTest(t, e, "new.go", "package prompts\n")
	code, out := runTestCLI(t, e, "", "inspect", "crew/new-session")
	if code != 2 || !strings.Contains(out, "composition declarations changed in Go") {
		t.Fatalf("stale definitions unnoticed: %d %s", code, out)
	}
}

func TestScenarioPathsStayInsideCheckout(t *testing.T) {
	e := testEditor(t)
	if _, err := e.operation(t.Context(), operationRequest{Op: "scenario-save", ID: "../outside", Event: "crew/new-session"}); err == nil {
		t.Fatal("accepted path traversal")
	}
	if _, err := os.Stat(filepath.Join(e.root.Name(), "outside.json")); !os.IsNotExist(err) {
		t.Fatal("wrote outside the scenarios folder")
	}
}

func TestRefreshReloadsChangedGoDeclarationsWithoutRestart(t *testing.T) {
	e := testEditor(t)
	files, err := filepath.Glob("../../internal/prompts/*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		writeTest(t, e, filepath.Base(name), string(data))
	}
	protocolDir := filepath.Join(e.repo, "internal/protocol")
	if err := os.MkdirAll(protocolDir, 0755); err != nil {
		t.Fatal(err)
	}
	protocolTypes, err := os.ReadFile("../../internal/protocol/generated.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(protocolDir, "generated.go"), protocolTypes, 0644); err != nil {
		t.Fatal(err)
	}
	generator, err := os.ReadFile("../promptgen/main.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cmd/promptgen", "native/crates/attn-app/prompts"} {
		if err := os.MkdirAll(filepath.Join(e.repo, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(e.repo, "cmd/promptgen/main.go"), generator, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.repo, "go.mod"), []byte("module github.com/victorarias/attn\n\ngo 1.25.3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	operate(t, e, operationRequest{Op: "refresh"})
	var declaration string
	for _, name := range files {
		data, err := e.root.ReadFile(filepath.Base(name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `On("new-session",`) {
			declaration = filepath.Base(name)
			writeTest(t, e, declaration, strings.Replace(string(data), `On("new-session",`, `On("new-session-renamed",`, 1))
			break
		}
	}
	if declaration == "" {
		t.Fatal("wake declaration missing")
	}
	if _, err := e.operation(t.Context(), operationRequest{Op: "inspect", Event: "crew/new-session"}); err == nil {
		t.Fatal("stale catalog accepted")
	}
	operate(t, e, operationRequest{Op: "refresh"})
	result := operate(t, e, operationRequest{Op: "inspect", Event: "crew/new-session-renamed", Values: prompts.Values{}}).(map[string]any)
	if result["result"].(prompts.Result).Text == "" {
		t.Fatal("refreshed event not rendered")
	}
	if _, err := e.operation(t.Context(), operationRequest{Op: "inspect", Event: "crew/new-session"}); err == nil {
		t.Fatal("old event survived refresh")
	}
	writeTest(t, e, declaration, "invalid Go syntax")
	if _, err := e.operation(t.Context(), operationRequest{Op: "refresh"}); err == nil {
		t.Fatal("bad Go accepted")
	}
}

func TestScenarioBindingsRejectCyclesAndDuplicateValues(t *testing.T) {
	e := testEditor(t)
	manifestTest(t, e, prompts.Recipient{ID: "loop", Events: []prompts.Event{prompts.On("render", "user_message", "", prompts.Input(prompts.ProducedBy(prompts.TextField("body", "Bound input"), "loop/render")))}})
	writeTest(t, e, "scenarios/loop.json", `{"version":1,"id":"loop","recipient":"loop","event":"render","values":{},"inputs":{"body":"loop"}}`)
	result := operate(t, e, operationRequest{Op: "check"}).(map[string]any)
	if result["valid"] != false || !strings.Contains(result["scenarios"].([]scenarioCheck)[0].Error, "cycle") {
		t.Fatal("cycle accepted")
	}
	_, err := e.operation(t.Context(), operationRequest{Op: "scenario-save", ID: "duplicate", Event: "loop/render", Values: prompts.Values{"body": "literal"}, Inputs: map[string]string{"body": "loop"}})
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate binding accepted: %v", err)
	}
}
