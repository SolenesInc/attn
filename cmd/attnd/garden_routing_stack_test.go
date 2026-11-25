package main_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestGardenV2RoutesOutpostWorkFromTheCallersCheckout(t *testing.T) {
	o := newOutpostTools(t, fakeagent.Claude)
	requireStdout(t, o.home.Attn("settings", "set", "garden_v2_enabled", "true"), "true")
	launch := o.outpost.Path("launch")
	current := o.outpost.Path("current")
	for _, repo := range []string{launch, current} {
		gitRepo(t, repo)
	}
	currentSubdir := filepath.Join(current, "subdir")
	if err := os.Mkdir(currentSubdir, 0o755); err != nil {
		t.Fatal(err)
	}

	run := func(stack *testworld.Stack, dir, terminal string, args []string, stdin string, into any) {
		t.Helper()
		result := stack.Run(testworld.Invocation{Args: args, Terminal: terminal, Dir: dir, Stdin: stdin})
		if result.Code != 0 {
			t.Fatalf("attn %q in %s: %+v", args, dir, result)
		}
		result.JSON(t, into)
	}
	checkBed := func(id, name string) {
		t.Helper()
		seed := showSeed(t, o.home, "", id).Seed
		if got := protocol.Deref(seed.Bed); got != name {
			t.Errorf("seed %q belongs to bed %q, want %q", seed.Title, got, name)
		} else {
			t.Logf("seed %q belongs to bed %q", seed.Title, got)
		}
	}

	var bed protocol.SeedBedResult
	run(o.home, launch, "", []string{"seed", "bed", "add", "Launch", "--repo", "launch", "--json"}, "", &bed)
	run(o.home, launch, "", []string{"seed", "bed", "add", "Current", "--repo", "current", "--json"}, "", &bed)

	session, agent := o.spawn(fakeagent.Claude, launch)
	terminal := o.pair.App.Terminal(session)
	var planted protocol.SeedPlantResult
	run(o.outpost, currentSubdir, terminal, []string{"seed", "plant", "Plant after changing checkout", "--json"}, "", &planted)
	checkBed(planted.ID, "Current")
	var plot protocol.SeedPlotResult
	run(o.outpost, currentSubdir, terminal, []string{"seed", "plot", "--json"}, `{"title":"Plot after changing checkout","children":[{"title":"Child uses root bed"}]}`, &plot)
	checkBed(plot.Crown.ID, "Current")
	checkBed(plot.Children[0].ID, "Current")

	run(o.outpost, currentSubdir, "", []string{"seed", "plant", "Plant in an outpost shell", "--json"}, "", &planted)
	checkBed(planted.ID, "Current")

	currentAlias := o.outpost.Path("current-link")
	if err := os.Symlink(current, currentAlias); err != nil {
		t.Fatal(err)
	}
	run(o.outpost, launch, terminal, []string{"seed", "bed", "set", "Current", "--repo", "../current-link", "--json"}, "", &bed)
	if want := o.pair.Host + ":" + current; len(bed.Bed.Repos) != 1 || bed.Bed.Repos[0] != want {
		t.Errorf("relative outpost repo saved as %v, want %q", bed.Bed.Repos, want)
	}
	run(o.outpost, currentSubdir, "", []string{"seed", "plant", "Plant with a relative outpost binding", "--json"}, "", &planted)
	checkBed(planted.ID, "Current")

	run(o.home, current, "", []string{"seed", "bed", "add", "Home current", "--repo", current, "--json"}, "", &bed)
	if want := "home:" + current; len(bed.Bed.Repos) != 1 || bed.Bed.Repos[0] != want {
		t.Errorf("home absolute repo saved as %v, want %q", bed.Bed.Repos, want)
	}
	run(o.home, currentSubdir, "", []string{"seed", "plant", "Plant from the home at the same path", "--json"}, "", &planted)
	checkBed(planted.ID, "Home current")
	run(o.outpost, currentSubdir, "", []string{"seed", "plant", "Plant from the outpost at the same path", "--json"}, "", &planted)
	checkBed(planted.ID, "Current")

	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	aliasFromHome, err := filepath.Rel(homeDir, currentAlias)
	if err != nil {
		t.Fatal(err)
	}
	run(o.outpost, launch, terminal, []string{"seed", "bed", "set", "Current", "--repo", "~/" + aliasFromHome, "--json"}, "", &bed)
	if want := o.pair.Host + ":" + current; len(bed.Bed.Repos) != 1 || bed.Bed.Repos[0] != want {
		t.Errorf("outpost tilde alias saved as %v, want %q", bed.Bed.Repos, want)
	}
	run(o.outpost, launch, terminal, []string{"seed", "bed", "set", "Launch", "--repo", ".", "--json"}, "", &bed)
	run(o.home, launch, "", []string{"seed", "bed", "add", "Home launch", "--repo", launch, "--json"}, "", &bed)

	claudeToolEndpoint(t, agent).structured(t, "seed_plant", map[string]any{"title": "Tool call routes to its outpost checkout"}, &planted)
	checkBed(planted.ID, "Launch")

	delegated, err := o.home.Client().Delegate(protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "route-outpost-root", Cwd: currentSubdir,
		Host: &o.pair.Host, Agent: protocol.Ptr("claude"),
		Checkout:   &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: "main"},
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Delegate into the current checkout")},
	})
	if err != nil {
		t.Fatal(err)
	}
	checkBed(delegated.SeedID, "Current")
}
