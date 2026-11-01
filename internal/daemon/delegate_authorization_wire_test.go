package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func TestADelegationsAuthorizationIsSavedOnTheSeedAndDeliveredInEveryOpening(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	everyday := []protocol.DelegationChoice{{ID: "default", Name: "Everyday", Selection: protocol.DelegationSelection{Harness: "codex"}}}
	if saved := savePreferences(app, protocol.DelegationPreferences{
		Enabled: true,
		Roles: []protocol.DelegationRole{{ID: "build", Name: "Builder", Enabled: true, Instructions: "Build it with care.",
			DefaultChoiceID: "default", Choices: everyday}},
		Fallback: protocol.DelegationFallback{Selection: protocol.DelegationSelection{Harness: "codex"}},
	}); !saved.Success {
		t.Fatalf("saving the role: %s", protocol.Deref(saved.Error))
	}
	cwd := registerDelegationCaller(t, w, cli, "caller")
	seed := plantDelegationSeed(t, cli, "caller", "Search endpoint")
	words := "You may land on main. Ask me before releasing."
	line := "Authorization from the user for this task, set by caller: " + words

	request := delegateAtSeed("caller", cwd, seed)
	request.Agent, request.Role = nil, protocol.Ptr("build")
	request.Authorization = protocol.Ptr(words)
	first, err := cli.Delegate(request)
	if err != nil {
		t.Fatal(err)
	}
	opening := w.Launched(string(first.SessionID)).Prompted()
	role, authorization := strings.Index(opening, "Build it with care."), strings.Index(opening, line)
	if role < 0 || authorization < role {
		t.Errorf("the delegate's opening is %q; want the role block, then %q", opening, line)
	}

	shown, err := cli.SeedShow("", seed)
	if err != nil {
		t.Fatal(err)
	}
	if got := shown.Seed.Authorization; got == nil || got.Text != words || got.SetBy.Ref != "session:caller" || got.SetAt == "" {
		t.Errorf("the seed shows authorization %+v; want %q set by session:caller with a time", got, words)
	}
	assertOneAuthorizationEntry(t, cli, seed, words)

	handover := delegateAtSeed("caller", cwd, seed)
	handover.Assignment.Handover = &protocol.DelegateHandover{Note: protocol.Ptr("Finish the endpoint.")}
	second, err := cli.Delegate(handover)
	if err != nil {
		t.Fatal(err)
	}
	if opening := w.Launched(string(second.SessionID)).Prompted(); !strings.HasSuffix(strings.TrimSpace(opening), line) {
		t.Errorf("the successor's opening is %q; want it to end with the seed's authorization %q", opening, line)
	}
	assertOneAuthorizationEntry(t, cli, seed, words)

	fresh := delegateFrom("caller", cwd, "Tidy the README", fakeagent.Codex)
	fresh.Label, fresh.Authorization = protocol.Ptr("readme"), protocol.Ptr("Ask me before pushing.")
	readme, err := cli.Delegate(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if opening := w.Launched(string(readme.SessionID)).Prompted(); !strings.HasSuffix(strings.TrimSpace(opening), "set by caller: Ask me before pushing.") {
		t.Errorf("a delegation that plants its seed opened with %q; want the authorization line last", opening)
	}
	unauthorized := delegateFrom("caller", cwd, "Rename the module", fakeagent.Codex)
	unauthorized.Label = protocol.Ptr("rename")
	plain, err := cli.Delegate(unauthorized)
	if err != nil {
		t.Fatal(err)
	}
	if opening := w.Launched(string(plain.SessionID)).Prompted(); strings.Contains(opening, "Authorization from the user") {
		t.Errorf("a delegation without an authorization opened with %q; want no authorization line", opening)
	}
}

func assertOneAuthorizationEntry(t *testing.T, cli *client.Client, seedID, words string) {
	t.Helper()
	log, err := cli.SeedNotes("", seedID, client.SeedNotesQuery{Kind: garden.NoteKindAuthorization})
	if err != nil {
		t.Fatal(err)
	}
	if len(log.Notes) != 1 || log.Notes[0].Body != words || log.Notes[0].Author.Ref != "session:caller" {
		t.Errorf("the seed's authorization entries are %+v; want one, %q by session:caller", log.Notes, words)
	}
}
