package apps

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/docstore"
)

func TestNamingRules(t *testing.T) {
	type row struct {
		validate func(string) error
		kind     string
		name     string
		ok       bool
		mentions []string
	}
	app := func(name string, ok bool, mentions ...string) row {
		return row{ValidateName, "app", name, ok, mentions}
	}
	view := func(name string, ok bool, mentions ...string) row {
		return row{ValidateViewName, "view", name, ok, mentions}
	}
	rows := []row{
		app("approval-gate", true),
		app("a", true),
		app("app2", true),
		app("standup-digest-v2", true),
		app("9lives", true),
		app("runtime-monitor", true),
		app("my-runtime", true),
		app(strings.Repeat("a", MaxNameLength), true),
		app("", false),
		app("-leading", false),
		app("Approval", false),
		app("with_underscore", false),
		app("with space", false),
		app("app/name", false),
		app("app:name", false),
		app(strings.Repeat("a", MaxNameLength+1), false, "65", "64"),
		view("approvals", true),
		view("a", true),
		view("pending-v2", true),
		view("9lives", true),
		view("runtime", true),
		view("status", true),
		view("", false),
		view("-leading", false),
		view("Approvals", false),
		view("with_underscore", false),
		view("with space", false),
		view("app/name", false),
		view("..", false),
		view(strings.Repeat("a", MaxViewNameLength+1), false, "65", "64"),
	}
	for _, reserved := range ReservedNames() {
		rows = append(rows, app(reserved, false, append([]string{"reserved"}, ReservedNames()...)...))
	}
	if !slices.Contains(ReservedNames(), "runtime") {
		t.Fatalf("reserved names %v do not include runtime, which collides with the shared runtime", ReservedNames())
	}

	for _, r := range rows {
		err := r.validate(r.name)
		if r.ok {
			if err != nil {
				t.Errorf("%s name %q refused: %v", r.kind, r.name, err)
			}
			if r.kind == "app" {
				if err := docstore.ValidateNamespace(Namespace(r.name)); err != nil {
					t.Errorf("docstore rejects the namespace for app %q: %v", r.name, err)
				}
			}
			continue
		}
		if err == nil {
			t.Errorf("%s name %q accepted", r.kind, r.name)
			continue
		}
		for _, want := range r.mentions {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusing %s name %q says %q, which does not mention %q", r.kind, r.name, err, want)
			}
		}
	}
}
