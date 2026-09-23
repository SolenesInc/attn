package delegationprefs

import (
	"slices"
	"testing"
)

func TestChangesReportReorderedRolesAndAlternatives(t *testing.T) {
	choice := func(id string) Choice { return Choice{ID: id, Name: id, When: "x"} }
	before := Config{Roles: []Role{
		{ID: "build", Name: "Build", DefaultChoiceID: "a", Choices: []Choice{choice("a"), choice("b"), choice("c")}},
		{ID: "review", Name: "Review", DefaultChoiceID: "a", Choices: []Choice{choice("a")}},
	}}
	after := Config{Roles: []Role{before.Roles[1], before.Roles[0]}}
	after.Roles[1].Choices = []Choice{choice("a"), choice("c"), choice("b")}
	got := Changes(before, after)
	for _, want := range []string{"reordered roles", "build: reordered alternatives"} {
		if !slices.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if len(Changes(before, before)) != 0 {
		t.Fatal("an unchanged table reported changes")
	}
}
