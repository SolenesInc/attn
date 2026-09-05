package prompts

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

func testCatalog(t *testing.T, files fstest.MapFS, body Node) *Catalog {
	t.Helper()
	c, err := New(files, Recipient{ID: "agent", Events: []Event{On("start", "instructions", "", body)}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRenderPreservesOrderAndExplainsBothBranches(t *testing.T) {
	name := Trimmed(TextField("name", ""))
	extra := FlagField("extra", "")
	files := fstest.MapFS{
		"named.md":     {Data: []byte("Hello {{name}}.\n")},
		"anonymous.md": {Data: []byte("Hello stranger.\n")},
		"extra.md":     {Data: []byte("Extra.\n")},
	}
	c := testCatalog(t, files, Compose(
		Choose(Present(name), Use("named", "named.md", Bind("name", Input(name))), Use("anonymous", "anonymous.md")),
		When(Enabled(extra), Use("extra", "extra.md")),
	))
	result, err := c.Render("agent", "start", Values{"name": "  Ada  ", "extra": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Hello Ada.\n\nExtra." {
		t.Fatalf("rendered %q", result.Text)
	}
	choice := result.Trace.Children[0]
	if !choice.Children[0].Selected || choice.Children[1].Selected || choice.Reason != "name is present" {
		t.Fatalf("choice trace: %+v", choice)
	}
	if choice.Children[1].Source != "anonymous.md" || choice.Children[1].Text != "" {
		t.Fatalf("skipped source should remain inspectable: %+v", choice.Children[1])
	}
	result, err = c.Render("agent", "start", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Hello stranger." || result.Trace.Children[1].Selected {
		t.Fatalf("omitted inputs: %+v", result)
	}
}

func TestSubstitutionsAreLiteralAndKeepSourceProvenance(t *testing.T) {
	value := TextField("value", "")
	files := fstest.MapFS{
		"outer.md":  {Data: []byte("{{shared}}\n{{value}}\n{{quoted}}\n{{value}}\n")},
		"shared.md": {Data: []byte("Shared instructions.\n")},
	}
	c := testCatalog(t, files, Use("outer", "outer.md",
		Bind("shared", Use("shared", "shared.md")), Bind("value", Input(value)), Bind("quoted", Quoted(value))))
	input := "{{shared}} $1 \\ backticks ` command $(something)\n\"quote\""
	result, err := c.Render("agent", "start", Values{"value": input})
	if err != nil {
		t.Fatal(err)
	}
	want := "Shared instructions.\n" + input + "\n" + strconv.Quote(input) + "\n" + input
	if result.Text != want {
		t.Fatalf("substitution changed literal input: %q", result.Text)
	}
	if result.Trace.Children[0].ID != "shared" || result.Trace.Children[0].Source != "shared.md" {
		t.Fatalf("lost inserted source: %+v", result.Trace)
	}
}

func TestCatalogValidatesUnselectedBranches(t *testing.T) {
	files := fstest.MapFS{"ok.md": {Data: []byte("Okay.\n")}, "slot.md": {Data: []byte("{{missing}}\n")}, "code.md": {Data: []byte("{{if .flag}}\n")}}
	for _, test := range []struct {
		name string
		body Node
		want string
	}{
		{"missing file", When(Enabled(FlagField("off", "")), Use("missing", "missing.md")), "missing.md"},
		{"missing binding", Use("slot", "slot.md"), "missing bindings for missing"},
		{"unused binding", Use("ok", "ok.md", Bind("unused", Input(TextField("x", "")))), "unused or duplicate binding"},
		{"conflicting identity", Compose(Use("same", "ok.md"), Use("same", "slot.md")), "conflicting definitions"},
		{"condition type", When(Enabled(TextField("wrong", "")), Use("ok", "ok.md")), "requires a flag input"},
		{"template logic", Use("code", "code.md"), "only {{name}} substitutions"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(files, Recipient{ID: "agent", Events: []Event{On("start", "instructions", "", test.body)}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v; want %s", err, test.want)
			}
		})
	}
}

func TestReuseFragmentWithDifferentBindings(t *testing.T) {
	first, second := TextField("first", ""), TextField("second", "")
	c := testCatalog(t, fstest.MapFS{"greeting.md": {Data: []byte("Hello {{name}}.\n")}}, Compose(
		Use("greeting", "greeting.md", Bind("name", Input(first))),
		Use("greeting", "greeting.md", Bind("name", Input(second))),
	))
	result, err := c.Render("agent", "start", Values{"first": "Ada", "second": "Grace"})
	if err != nil || result.Text != "Hello Ada.\n\nHello Grace." {
		t.Fatalf("fragment reuse: %+v, %v", result, err)
	}
}

func TestRenderRejectsInvalidInputs(t *testing.T) {
	value := TextField("value", "")
	c := testCatalog(t, fstest.MapFS{}, Compose(Input(value), When(Enabled(FlagField("flag", "")), Compose())))
	for _, test := range []struct {
		values Values
		want   string
	}{
		{nil, "missing input value"},
		{Values{"typo": "anything"}, "unknown input"},
		{Values{"flag": "yes"}, "needs true or false"},
	} {
		_, err := c.Render("agent", "start", test.values)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("got %v; want %s", err, test.want)
		}
	}
}

func TestCatalogDoesNotRetainMutableDeclarations(t *testing.T) {
	input := TextField("value", "")
	recipient := Recipient{ID: "agent", Events: []Event{On("start", "instructions", "", Compose(Input(input)))}}
	c, err := New(fstest.MapFS{}, recipient)
	if err != nil {
		t.Fatal(err)
	}
	recipient.Events[0].Body.Children[0].Field.Name = "changed"
	copy := c.Recipients()
	copy[0].Events[0].Body.Children[0].Field.Name = "also_changed"
	result, err := c.Render("agent", "start", Values{"value": "intact"})
	if err != nil || result.Text != "intact" {
		t.Fatalf("declaration mutation reached catalog: %+v, %v", result, err)
	}
}

func TestLaunchScenarioSelectsSourcesAndPreservesInput(t *testing.T) {
	values := Values{"notebook_root": " /tmp/book ", "self_report_pull_requests": "false", "workflow_enabled": "true", "garden_available": "true", "crew_priming": " Crew {{literal}}. "}
	result, err := Builtin().Render("session", "launch", values)
	if err != nil {
		t.Fatal(err)
	}
	var selected, skipped []string
	var visit func(Trace)
	visit = func(trace Trace) {
		if trace.Kind == "text" {
			if trace.Selected {
				selected = append(selected, trace.ID)
			} else {
				skipped = append(skipped, trace.ID)
			}
		}
		for _, child := range trace.Children {
			visit(child)
		}
	}
	visit(result.Trace)
	if !reflect.DeepEqual(selected, []string{"session.chief", "delegation.boundary", "session.garden"}) {
		t.Fatalf("selected sources: %v", selected)
	}
	if !reflect.DeepEqual(skipped, []string{"session.agent", "delegation.boundary", "session.workflow", "session.pull-request-guidance"}) {
		t.Fatalf("skipped sources: %v", skipped)
	}
	if !strings.HasSuffix(result.Text, "\n\nCrew {{literal}}.") {
		t.Fatalf("crew input was altered: %q", result.Text)
	}
	if values["crew_priming"] != " Crew {{literal}}. " {
		t.Fatal("render mutated caller's inputs")
	}
}

func BenchmarkLaunchInstructions(b *testing.B) {
	launch := Launch{NotebookRoot: "/tmp/notebook", Garden: true, Crew: "Crew instructions."}
	b.ReportAllocs()
	for b.Loop() {
		_ = launch.Instructions()
	}
}

func TestVerbatimAndSplitSourcePreserveBytes(t *testing.T) {
	files := fstest.MapFS{"source.md": {Data: []byte(" system \n{{USER}}\n{{VALUE}}\n")}}
	c := testCatalog(t, files, Compose(
		Trim(Part(Use("system", "source.md"), "{{USER}}", 0)),
		Trim(Part(Use("user", "source.md", Bind("VALUE", Input(TextField("value", "")))), "{{USER}}", 1)),
	))
	result, err := c.Render("agent", "start", Values{"value": "literal {{USER}}"})
	if err != nil || result.Text != "system\n\nliteral {{USER}}" {
		t.Fatalf("split: %+v %v", result, err)
	}
	raw := testCatalog(t, files, Document("document", "source.md"))
	result, err = raw.Render("agent", "start", nil)
	if err != nil || result.Text != string(files["source.md"].Data) {
		t.Fatalf("verbatim: %+v %v", result, err)
	}
	files["source.md"].Data = []byte("no delimiter")
	_, err = New(files, Recipient{ID: "agent", Events: []Event{On("start", "system", "", Part(Use("part", "source.md"), "{{USER}}", 0))}})
	if err == nil {
		t.Fatal("missing channel delimiter accepted")
	}
}

func TestJoinRetainsEmptySlotsAndExactRetainsFinalNewline(t *testing.T) {
	c := testCatalog(t, fstest.MapFS{"text.md": {Data: []byte("line\n")}}, Join("|", Compose(), Exact(Use("line", "text.md")), Compose()))
	result, err := c.Render("agent", "start", nil)
	if err != nil || result.Text != "|line\n|" {
		t.Fatalf("join: %+v %v", result, err)
	}
}
