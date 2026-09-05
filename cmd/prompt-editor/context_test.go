package main

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/prompts"
)

func contextFixture(t *testing.T) (*editor, string) {
	t.Helper()
	e := testEditor(t)
	writeTest(t, e, "content/rule.md", "# Shared instructions\n\nOld rule.\n\n## Unchanged\n\nKeep this full section.\n")
	writeTest(t, e, "content/preamble.md", "Old preamble.")
	writeTest(t, e, "content/optional.md", "Conditional instructions.")
	writeTest(t, e, "content/reference.md", "Read this related reference.")
	manifestTest(t, e,
		prompts.Recipient{ID: "writer", Events: []prompts.Event{
			prompts.On("rules", "reference", "Shared rules", prompts.Document("rules", "content/rule.md")),
		}},
		prompts.Recipient{ID: "session", Events: []prompts.Event{
			prompts.On("start", "launch_instructions", "A composed session", prompts.Compose(
				prompts.Document("rules", "content/rule.md"),
				prompts.Input(prompts.ProducedBy(prompts.TextField("preamble", "Session preamble"), "producer/preamble")),
				prompts.When(prompts.Enabled(prompts.FlagField("extra", "Optional guidance")), prompts.Document("optional", "content/optional.md")))),
		}},
		prompts.Recipient{ID: "producer", Events: []prompts.Event{
			prompts.On("preamble", "input", "Preamble producer", prompts.Document("preamble", "content/preamble.md")),
		}},
		prompts.Recipient{ID: "other", Events: []prompts.Event{
			prompts.On("start", "user_message", "Needs a task", prompts.Compose(prompts.Document("rules", "content/rule.md"), prompts.Input(prompts.TextField("task", "Task from caller")))),
		}},
		prompts.Recipient{ID: "reference", Events: []prompts.Event{
			prompts.On("guide", "reference", "Semantically related", prompts.Document("guide", "content/reference.md")),
		}},
	)
	writeTest(t, e, "scenarios/ordinary.json", `{"version":1,"id":"ordinary","description":"Ordinary session","recipient":"session","event":"start","values":{"extra":"false"},"inputs":{"preamble":"provided"}}`)
	writeTest(t, e, "scenarios/provided.json", `{"version":1,"id":"provided","recipient":"producer","event":"preamble","values":{}}`)
	gitTest(t, e, "init", "-b", "next")
	return e, commitTest(t, e)
}

func readContext(t *testing.T, e *editor, q operationRequest) contextReport {
	t.Helper()
	q.Op = "context"
	return operate(t, e, q).(contextReport)
}

func contextSample(t *testing.T, report contextReport, label string) contextScenario {
	t.Helper()
	for _, sample := range report.Scenarios {
		if sample.Label == label {
			return sample
		}
	}
	t.Fatalf("sample %q not found", label)
	return contextScenario{}
}

func TestAuthoringContextIncludesSharedConsumersProducersAndGaps(t *testing.T) {
	e, base := contextFixture(t)
	writeTest(t, e, "content/rule.md", "# Shared instructions\n\nNew rule.\n\n## Unchanged\n\nKeep this full section.\n")
	writeTest(t, e, "content/preamble.md", "New preamble.")
	beforeStatus := gitTest(t, e, "status", "--porcelain")
	report := readContext(t, e, operationRequest{ID: "writer/rules", Base: base})
	if !report.Sources["content/optional.md"].BaseSameAsCurrent || report.Sources["content/optional.md"].Base != nil {
		t.Fatal("unchanged base source was duplicated or lost")
	}
	var events []string
	for _, event := range report.Events {
		events = append(events, event.Event)
	}
	if !reflect.DeepEqual(events, []string{"other/start", "producer/preamble", "session/start", "writer/rules"}) {
		t.Fatalf("incomplete structural context: %v", events)
	}
	if !reflect.DeepEqual(report.UnrenderedEvents, []string{"other/start"}) || !reflect.DeepEqual(report.UnrenderedSources, []string{"content/optional.md"}) {
		t.Fatalf("missing context was hidden: %v / %v", report.UnrenderedEvents, report.UnrenderedSources)
	}
	if !reflect.DeepEqual(report.UnrenderedBaseEvents, report.UnrenderedEvents) || !reflect.DeepEqual(report.UnrenderedBaseSources, report.UnrenderedSources) {
		t.Fatal("base coverage gaps were hidden")
	}
	sample := contextSample(t, report, "Ordinary session")
	if !strings.Contains(sample.Current.Text, "New rule.") || !strings.Contains(sample.Current.Text, "New preamble.") || !strings.Contains(sample.Base.Text, "Old preamble.") || sample.Current.Delivery != "launch_instructions" {
		t.Fatalf("wrong composed context: %+v", sample)
	}
	if !strings.Contains(report.Sources["content/rule.md"].Current.Text, "Keep this full section.") || !strings.Contains(sample.Diff, "+New rule.") {
		t.Fatal("full text or comparison missing")
	}
	if !slices.Contains(report.Sources["content/preamble.md"].Uses, usage{Event: "session/start", Via: "producer/preamble"}) {
		t.Fatal("producer use not explained")
	}
	if !strings.Contains(report.Workflow, "before adding instructions") || report.Guide != "docs/prompt-authoring.md" || len(report.Limits) == 0 {
		t.Fatal("authoring context omitted its workflow or limits")
	}
	if gitTest(t, e, "status", "--porcelain") != beforeStatus || gitTest(t, e, "rev-parse", "HEAD") != base {
		t.Fatal("context mutated the checkout")
	}
}

func TestAuthoringContextExpandsRelatedInstructionsAndCustomInputs(t *testing.T) {
	e, _ := contextFixture(t)
	code, out := runTestCLI(t, e, "", "context", "--scenario", "ordinary", "--set", "extra=true", "--include", "reference/guide", "--include", "content/rule.md")
	var report contextReport
	if code != 0 || json.Unmarshal([]byte(out), &report) != nil {
		t.Fatalf("context CLI failed: %d %s", code, out)
	}
	custom, saved := contextSample(t, report, "Custom inputs"), contextSample(t, report, "Ordinary session")
	if !strings.Contains(custom.Current.Text, "Conditional instructions.") || strings.Contains(saved.Current.Text, "Conditional instructions.") {
		t.Fatal("custom inputs ignored or saved scenario replaced")
	}
	if len(report.UnrenderedSources) != 0 || report.Sources["content/reference.md"].Current.Text != "Read this related reference." {
		t.Fatal("related or newly rendered instructions missing")
	}
	for _, args := range [][]string{{"context"}, {"context", "wrong/path"}, {"context", "writer/rules", "--include", "missing"}, {"context", "--scenario", "missing"}, {"context", "content/rule.md", "--set", "task=hi"}} {
		if code, out := runTestCLI(t, e, "", args...); code != 2 {
			t.Fatalf("invalid context accepted: %v: %d %s", args, code, out)
		}
	}
}

func TestAuthoringContextFreezesAllDraftEditsAndScenarios(t *testing.T) {
	e, base := contextFixture(t)
	d := operate(t, e, operationRequest{Op: "draft-create", Title: "Clarify rules", Focus: &focus{Event: "writer/rules", Values: prompts.Values{}, BaseCommit: base}}).(sharedDraft)
	d = putTest(t, e, d, "content/rule.md", "Revised shared rule.")
	d = putTest(t, e, d, "content/reference.md", "Revised related guide.")
	draft := readContext(t, e, operationRequest{DraftID: d.ID})
	if !slices.Contains(draft.Scope, "content/reference.md") || draft.Sources["content/reference.md"].Current.Text != "Revised related guide." {
		t.Fatal("draft context was limited to the selected event")
	}
	r := operate(t, e, operationRequest{Op: "review-create", DraftID: d.ID, Revision: d.Revision}).(review)
	before := readContext(t, e, operationRequest{ReviewID: r.ID})
	d = operate(t, e, operationRequest{Op: "draft-get", ID: d.ID}).(sharedDraft)
	putTest(t, e, d, "content/reference.md", "Later draft edit.")
	writeTest(t, e, "content/preamble.md", "Later checkout preamble.")
	writeTest(t, e, "scenarios/ordinary.json", `{"version":1,"id":"ordinary","description":"Changed inputs","recipient":"session","event":"start","values":{"extra":"true"},"inputs":{"preamble":"provided"}}`)
	after := readContext(t, e, operationRequest{ReviewID: r.ID})
	if !reflect.DeepEqual(before, after) || before.BaseCommit != base {
		t.Fatal("review context followed later edits or lost its pinned base")
	}
	if !slices.Contains(after.Scope, "content/reference.md") {
		t.Fatal("review lost the draft's complete changed-source scope")
	}
	_, err := e.withState(func(root *os.Root) (any, error) {
		r.Snapshot.EditedSources = nil
		return nil, writeJSON(root, "reviews/"+r.ID+".json", r)
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy := readContext(t, e, operationRequest{ReviewID: r.ID})
	if !slices.Contains(legacy.Scope, "content/reference.md") {
		t.Fatal("older review scope was silently narrowed")
	}
	_, err = e.withState(func(root *os.Root) (any, error) {
		r.Focus.BaseCommit = ""
		return nil, writeJSON(root, "reviews/"+r.ID+".json", r)
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy = readContext(t, e, operationRequest{ReviewID: r.ID})
	if !strings.Contains(strings.Join(legacy.Limits, " "), "older review") {
		t.Fatal("unknown older review scope was not disclosed")
	}
}

func TestAuthoringContextKeepsAppliedChangesInSharedComparison(t *testing.T) {
	e, base := contextFixture(t)
	d := operate(t, e, operationRequest{Op: "draft-create", Title: "Rules and references", Focus: &focus{Event: "writer/rules", Values: prompts.Values{}, BaseCommit: base}}).(sharedDraft)
	d = putTest(t, e, d, "content/reference.md", "Applied reference change.")
	d = operate(t, e, operationRequest{Op: "draft-apply", ID: d.ID, Revision: d.Revision}).(sharedDraft)
	r := operate(t, e, operationRequest{Op: "review-create", DraftID: d.ID, Revision: d.Revision}).(review)
	for _, q := range []operationRequest{{DraftID: d.ID}, {ReviewID: r.ID}} {
		report := readContext(t, e, q)
		if !slices.Contains(report.Scope, "content/reference.md") || report.Sources["content/reference.md"].Current.Text != "Applied reference change." {
			t.Fatal("applying a draft removed its changed sources from shared context")
		}
	}
}

func TestAuthoringContextRetainsRemovedAndUnavailableBaseText(t *testing.T) {
	e, base := contextFixture(t)
	snapshot, err := e.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := snapshot.load(nil)
	if err != nil {
		t.Fatal(err)
	}
	recipients := slices.DeleteFunc(catalog.Recipients(), func(r prompts.Recipient) bool { return r.ID == "reference" })
	manifestTest(t, e, recipients...)
	report := readContext(t, e, operationRequest{ID: "reference/guide", Base: base})
	if len(report.Scenarios) != 1 || report.Scenarios[0].Current.Status != "absent" || report.Scenarios[0].Base.Text != "Read this related reference." {
		t.Fatalf("removed prompt omitted: %+v", report.Scenarios)
	}
	if report.Sources["content/reference.md"].Current != nil || report.Sources["content/reference.md"].Base == nil {
		t.Fatal("removed source disappeared")
	}
	if err := e.root.Remove(prompts.ManifestPath); err != nil {
		t.Fatal(err)
	}
	legacy := commitTest(t, e)
	manifestTest(t, e, recipients...)
	report = readContext(t, e, operationRequest{ID: "writer/rules", Base: legacy})
	if !strings.Contains(report.Unavailable, "no catalog manifest") || !report.Sources["content/rule.md"].BaseSameAsCurrent || contextSample(t, report, "Ordinary session").Current.Status != "present" {
		t.Fatal("unavailable composition hid available source context")
	}
}
