package main_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func plant(t *testing.T, s *testworld.Stack, title string, args ...string) protocol.Seed {
	t.Helper()
	var seed protocol.Seed
	s.Attn(append([]string{"seed", "plant", title, "--json"}, args...)...).JSON(t, &seed)
	return seed
}

func plantPlot(t *testing.T, s *testworld.Stack, payload string) protocol.SeedPlotResult {
	t.Helper()
	var plot protocol.SeedPlotResult
	s.Run(testworld.Invocation{Args: []string{"seed", "plot", "--json"}, Stdin: payload}).JSON(t, &plot)
	return plot
}

func showSeed(t *testing.T, s *testworld.Stack, session, id string) protocol.SeedShowResult {
	t.Helper()
	var shown protocol.SeedShowResult
	s.Run(testworld.Invocation{Args: []string{"seed", "show", id, "--json"}, Session: session}).JSON(t, &shown)
	return shown
}

func seedAs(t *testing.T, s *testworld.Stack, session string, args ...string) string {
	t.Helper()
	ran := s.Run(testworld.Invocation{Args: append([]string{"seed"}, args...), Session: session})
	if ran.Code != 0 {
		t.Fatalf("attn seed %q exited %d: %s", args, ran.Code, ran.Stderr)
	}
	return ran.Stdout
}

func lineOf(t *testing.T, text, id string) (int, string) {
	t.Helper()
	for i, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, id+" ") {
			return i, line
		}
	}
	t.Fatalf("no line for %s:\n%s", id, text)
	return 0, ""
}

func TestTheGardenCommandsPrintWhatAgentsActOn(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	for _, row := range []struct {
		args []string
		want string
	}{
		{[]string{"seed", "harvest", "s-7k3f9m", "--when-merged", "-m", "done"}, "takes no -m"},
		{[]string{"seed", "harvest", "s-7k3f9m", "--when-merged", "s-2p4qxv"}, `"s-2p4qxv" is not a pull request url`},
		{[]string{"seed", "harvest", "s-7k3f9m", "--clear"}, "`attn seed harvest <id> --when-merged --clear`"},
		{[]string{"seed", "park", "s-7k3f9m", "--when-merged"}, "belongs to harvest"},
		{[]string{"seed", "attach", "s-7k3f9m"}, "name the document"},
		{[]string{"seed", "attach", "s-7k3f9m", "--repo", "attn"}, "name the document"},
		{[]string{"seed", "attach", "s-7k3f9m", "--notebook", "nb-7", "--url", "https://example.test"}, "--notebook and --url were all given"},
		{[]string{"seed", "attach", "s-7k3f9m", "--path", "plan.bin"}, "exactly one of --move or --copy"},
		{[]string{"seed", "attach", "s-7k3f9m", "--path", "plan.bin", "--move", "--copy"}, "exactly one of --move or --copy"},
		{[]string{"seed", "attach", "s-7k3f9m", "--path", "docs/plan.md", "--repo", "attn", "--copy"}, "apply only to a local --path without --repo"},
		{[]string{"seed", "detach", "s-7k3f9m", "--path", "report.pdf"}, "requires --to <destination>"},
		{[]string{"seed", "review", "show", "--model", "sonnet"}, `unknown flag "--model"`},
	} {
		verb := strings.Join(row.args[:2], " ")
		if row.args[1] == "review" {
			verb = strings.Join(row.args[:3], " ")
		}
		requireFailure(t, s.Attn(row.args...), verb+": ", row.want)
	}

	s.Start()
	app := s.App()

	t.Run("listings nest children under their plot and ready lists pickups", func(t *testing.T) {
		first := plantPlot(t, s, `{"title": "First plot", "children": [{"title": "First child"}]}`)
		second := plantPlot(t, s, `{"title": "Second plot", "children": [{"title": "Second child"}]}`)
		loose := plant(t, s, "Loose seed")

		nested := seedAs(t, s, "", "ls")
		plotAt, _ := lineOf(t, nested, second.Crown.ID)
		childAt, childRow := lineOf(t, nested, second.Children[0].ID)
		if childAt != plotAt+1 || !strings.Contains(childRow, "    Second child") {
			t.Errorf("ls does not nest the child under its plot:\n%s", nested)
		}
		flat := seedAs(t, s, "", "ls", "--flat")
		plotAt, _ = lineOf(t, flat, second.Crown.ID)
		childAt, childRow = lineOf(t, flat, second.Children[0].ID)
		if childAt > plotAt || strings.Contains(childRow, "    Second child") {
			t.Errorf("ls --flat nests:\n%s", flat)
		}

		ready := seedAs(t, s, "", "ready")
		var order []int
		for _, id := range []string{first.Crown.ID, first.Children[0].ID, second.Crown.ID, second.Children[0].ID, loose.ID} {
			at, _ := lineOf(t, ready, id)
			order = append(order, at)
		}
		if !slices.IsSorted(order) {
			t.Errorf("ready does not list each plot with its children before the loose seed:\n%s", ready)
		}
		if _, header := lineOf(t, ready, first.Crown.ID); !regexp.MustCompile(`^\S+ +` + first.Crown.StepSlug + ` +plot +`).MatchString(header) {
			t.Errorf("the plot's row does not mark it a plot: %q", header)
		}
		requireLines(t, "ready", ready, "\n3 ready in the garden — `attn seed tend <id>` claims one\n")

		found := s.Attn("seed", "plant", "Fix the leak", "--discovered-from", loose.ID)
		requireStdout(t, found, " Fix the leak\n")
		id, _, _ := strings.Cut(found.Stdout, " ")
		if edges := showSeed(t, s, "", id).Seed.Edges; !slices.Contains(edges, protocol.SeedEdge{Kind: "discovered-from", To: loose.ID}) {
			t.Errorf("a seed planted --discovered-from after its title has edges %+v", edges)
		}
	})

	t.Run("show puts the freshest handoff first and tend primes with it", func(t *testing.T) {
		carried := plant(t, s, "Carry this", "-m", "the plan")
		requireLines(t, "note", seedAs(t, s, "", "note", carried.ID, "-m", "ordinary progress"), "noted on "+carried.ID)
		requireLines(t, "handoff", seedAs(t, s, "", "note", carried.ID, "-m", "first line\nsecond line\n", "--handoff", "--member", "keel"),
			"handoff left on "+carried.ID+" — whoever tends it next reads this first")

		shown := seedAs(t, s, "", "show", carried.ID)
		if !strings.HasPrefix(shown, "handoff — Keel, ") || !strings.Contains(shown, "\n  first line\n  second line\n\n"+carried.ID+" ") {
			t.Errorf("show does not open with the indented handoff:\n%s", shown)
		}
		if strings.Count(shown, "first line") != 1 || !strings.Contains(shown, "ordinary progress") || !strings.Contains(shown, "\nthe plan\n") {
			t.Errorf("show repeats the handoff or drops the rest of the seed:\n%s", shown)
		}
		notes := seedAs(t, s, "", "notes", carried.ID)
		if !regexp.MustCompile(`(?m)^\S+ \S+  Keel  handoff$`).MatchString(notes) || !regexp.MustCompile(`(?m)^\S+ \S+  -$`).MatchString(notes) {
			t.Errorf("notes must label the handoff and nothing else:\n%s", notes)
		}

		for _, progress := range []string{"one", "two", "three", "four", "five"} {
			seedAs(t, s, "", "note", carried.ID, "-m", "progress "+progress)
		}
		requireLines(t, "show", seedAs(t, s, "", "show", carried.ID), "handoff — Keel, ", "progress five", "\n2 more — `attn seed notes "+carried.ID+"`\n")

		tended := seedAs(t, s, "", "tend", carried.ID, "--member", "alder")
		if !strings.HasPrefix(tended, carried.ID+" ("+carried.StepSlug+") is growing, tended by Alder\n") || !strings.Contains(tended, "handoff — Keel, ") ||
			!strings.Contains(tended, "  first line\n  second line\n") {
			t.Errorf("tend does not confirm the claim and then print the handoff:\n%s", tended)
		}

		quiet := plant(t, s, "Quiet seed")
		if out := seedAs(t, s, "", "show", quiet.ID) + seedAs(t, s, "", "tend", quiet.ID, "--member", "alder"); strings.Contains(out, "handoff") {
			t.Errorf("a seed without a handoff mentions one:\n%s", out)
		}
	})

	t.Run("search names each hit and says what it trimmed", func(t *testing.T) {
		hit := plant(t, s, "Find seeds by keyword", "-m", "Agents search the garden before planting, so duplicates get found")
		requireStdout(t, s.Attn("seed", "search", "duplicates", "planting"),
			"1 seed matches \"duplicates planting\"",
			"\n"+hit.ID+"  "+hit.StepSlug+"  Find seeds by keyword\n  planted  body  Agents search the garden before planting")
		if out := s.Attn("seed", "search", "duplicates").Stdout; strings.Contains(out, "max_results") {
			t.Errorf("an untrimmed search talks about the cap:\n%s", out)
		}

		for _, title := range []string{"Zanzibar north", "Zanzibar south", "Zanzibar east"} {
			plant(t, s, title)
		}
		requireStdout(t, s.Attn("seed", "search", "zanzibar", "--limit", "2"),
			"3 seeds match \"zanzibar\"", "\nshowing 2 of 3 — max_results=2, asked for 3. Narrow the query, or `--limit <n>` up to 1000.\n")

		var garden protocol.SeedListResult
		s.Attn("seed", "ls", "--json").JSON(t, &garden)
		requireStdout(t, s.Attn("seed", "search", "xyzzy"),
			"no seed matches \"xyzzy\" — searched "+strconv.Itoa(garden.Total)+" seeds: titles, bodies and every log entry\n")
	})

	t.Run("watch and unwatch say which watch still covers a seed", func(t *testing.T) {
		watcher := s.Spawn(app, fakeagent.Claude, s.Path("garden"), labelled("watcher"))
		claude := s.Launched(watcher)
		settle(app, watcher)
		plot := plant(t, s, "Watched plot")
		child := plant(t, s, "Watched child", "--part-of", plot.ID)

		requireLines(t, "watch", seedAs(t, s, watcher, "watch", plot.ID), "watching "+plot.ID+" and its descendants\n")
		requireLines(t, "show", seedAs(t, s, watcher, "show", child.ID), "watching via "+plot.ID+" (remove with attn seed unwatch "+plot.ID+")\n")
		if shown := showSeed(t, s, watcher, child.ID); !shown.Watching || !slices.Equal(shown.WatchingVia, []string{plot.ID}) {
			t.Errorf("show --json = watching %t via %q, want the plot's watch", shown.Watching, shown.WatchingVia)
		}
		requireLines(t, "watch", seedAs(t, s, watcher, "watch", child.ID), "watching "+child.ID+" and its descendants\n")
		if shown := seedAs(t, s, watcher, "show", child.ID); !regexp.MustCompile(`(?m)^watching +yes$`).MatchString(shown) {
			t.Errorf("show does not say the session watches the seed:\n%s", shown)
		}
		requireLines(t, "unwatch", seedAs(t, s, watcher, "unwatch", child.ID), "removed watch on "+child.ID+"\n", "watching via "+plot.ID+" (remove with attn seed unwatch "+plot.ID+")\n")
		requireLines(t, "unwatch", seedAs(t, s, watcher, "unwatch", child.ID), "no direct watch on "+child.ID+"\n", "attn seed unwatch "+plot.ID)
		requireLines(t, "unwatch", seedAs(t, s, watcher, "unwatch", plot.ID), "removed watch on "+plot.ID+"\n", "no remaining watch covers "+plot.ID+"\n")

		quiet, rung := plant(t, s, "Quietly noted"), plant(t, s, "Rung for attention")
		seedAs(t, s, watcher, "watch", quiet.ID)
		seedAs(t, s, watcher, "watch", rung.ID)
		seedAs(t, s, "", "note", quiet.ID, "-m", "progress nobody needs to see")
		seedAs(t, s, "", "note", rung.ID, "-m", "look here", "--ring")
		claude.Prompted()
		var inbox protocol.AgentInboxBatchResult
		s.Run(testworld.Invocation{Args: []string{"agent", "inbox", "--json"}, Session: watcher}).JSON(t, &inbox)
		if len(inbox.Items) != 1 || !strings.Contains(inbox.Items[0].Content, rung.ID) {
			t.Errorf("the watcher's inbox = %+v, want only the rung seed", inbox.Items)
		}
	})

	t.Run("closing a seed names what it freed and what it stranded", func(t *testing.T) {
		house := plantPlot(t, s, `{"title": "Paint the house", "children": [{"title": "Scrape the walls"}, {"title": "Prime the walls"}, {"title": "Paint the walls"}]}`)
		scrape, prime, paint := house.Children[0], house.Children[1], house.Children[2]
		scraped := seedAs(t, s, "", "harvest", scrape.ID, "-m", "scraped")
		requireLines(t, "harvest", scraped, scrape.ID+" ("+scrape.StepSlug+") is harvested — scraped\n")
		seedAs(t, s, "", "wither", prime.ID, "-m", "the paint primes itself")
		seedAs(t, s, "", "tend", paint.ID, "--member", "alder")
		tendedPlot := seedAs(t, s, "", "tend", house.Crown.ID, "--member", "alder")
		closed := seedAs(t, s, "", "harvest", house.Crown.ID, "-m", "good enough", "--member", "alder")
		requireLines(t, "harvest", closed, house.Crown.ID+" ("+house.Crown.StepSlug+") is harvested — good enough\n",
			"its plot still holds 1 open seed(s) — a closed plot over open work reads as done; close them too, or replant this one\n")

		shed := plantPlot(t, s, `{"title": "Build the shed", "children": [{"title": "Pour the slab"}]}`)
		poured := seedAs(t, s, "", "harvest", shed.Children[0].ID, "-m", "poured")
		built := seedAs(t, s, "", "harvest", shed.Crown.ID, "-m", "built")
		for _, out := range []string{scraped, tendedPlot, poured, built} {
			if strings.Contains(out, "open seed") || strings.Contains(out, "unblocked") {
				t.Errorf("a close with nothing stranded or freed warned:\n%s", out)
			}
		}

		pipe, water, wall := plant(t, s, "Lay the pipe"), plant(t, s, "Run water through it"), plant(t, s, "Paint the wall")
		seedAs(t, s, "", "link", pipe.ID, "blocks", water.ID)
		seedAs(t, s, "", "link", pipe.ID, "blocks", wall.ID)
		seedAs(t, s, "", "tend", wall.ID, "--member", "trellis")
		laid := seedAs(t, s, "", "harvest", pipe.ID, "-m", "laid")
		requireLines(t, "harvest", laid, "this unblocked 2 seed(s):\n", "`attn seed tend <id>` claims one\n")
		_, freed := lineOf(t, laid, "  "+water.ID)
		requireLines(t, "the free seed", freed, water.StepSlug, "Run water through it")
		_, held := lineOf(t, laid, "  "+wall.ID)
		requireLines(t, "the held seed", held, "Paint the wall — tended by Trellis")

		fence, gate := plant(t, s, "Mend the fence"), plant(t, s, "Hang the gate")
		seedAs(t, s, "", "link", fence.ID, "blocks", gate.ID)
		seedAs(t, s, "", "tend", gate.ID, "--member", "trellis")
		withered := seedAs(t, s, "", "wither", fence.ID, "-m", "no fence after all")
		requireLines(t, "wither", withered, "this unblocked 1 seed(s):\n", gate.ID)
		if strings.Contains(withered, "claims one") {
			t.Errorf("wither offers a claim on a seed somebody holds:\n%s", withered)
		}
	})

	t.Run("harvest when merged arms, shows and clears the condition", func(t *testing.T) {
		register(t, s, "shipper", "shipper")
		first, second, third := plant(t, s, "Ship the fix"), plant(t, s, "Ship the docs"), plant(t, s, "Ship the tests")
		for _, seed := range []protocol.Seed{first, second, third} {
			seedAs(t, s, "shipper", "tend", seed.ID)
		}
		requireLines(t, "arm", seedAs(t, s, "shipper", "harvest", first.ID, "--when-merged", "https://github.com/victorarias/attn/pull/118"),
			first.ID+" ("+first.StepSlug+") is dormant", "\nharvests when victorarias/attn#118 merges\n")
		requireLines(t, "arm", seedAs(t, s, "shipper", "harvest", third.ID, "--when-merged"), "\nharvests when victorarias/attn#118 merges\n")
		requireLines(t, "arm", seedAs(t, s, "shipper", "harvest", "--when-merged", second.ID, "https://github.com/victorarias/attn/pull/119"),
			"\nharvests when victorarias/attn#119 merges\n")

		shown := seedAs(t, s, "shipper", "show", first.ID)
		if !regexp.MustCompile(`(?m)^harvests when +victorarias/attn#118 merges$`).MatchString(shown) || !regexp.MustCompile(`(?m)^PR last checked +not yet$`).MatchString(shown) {
			t.Errorf("show does not say what the seed waits on:\n%s", shown)
		}
		listed := seedAs(t, s, "", "ls")
		_, row := lineOf(t, listed, second.ID)
		if !strings.HasSuffix(row, "Ship the docs  [harvests when victorarias/attn#119 merges]") {
			t.Errorf("the armed row = %q", row)
		}
		if strings.Count(listed, "[harvests when") != 3 {
			t.Errorf("unarmed rows carry a harvest suffix:\n%s", listed)
		}

		requireLines(t, "clear", seedAs(t, s, "shipper", "harvest", first.ID, "--when-merged", "--clear"), "\nharvest-on-merge cleared\n")
		if cleared := seedAs(t, s, "shipper", "show", first.ID); regexp.MustCompile(`(?m)^PR last checked `).MatchString(cleared) {
			t.Errorf("a cleared seed still waits:\n%s", cleared)
		}
	})

	t.Run("attach links documents and takes ownership of local files", func(t *testing.T) {
		seed := plant(t, s, "Keep the report")
		for _, args := range [][]string{
			{"--path", "docs/plans/x.md", "--repo", "attn"},
			{"--notebook", "nb-7"},
			{"--url", "https://example.test/pr/1"},
		} {
			seedAs(t, s, "", append([]string{"attach", seed.ID}, args...)...)
		}
		var linked []string
		for _, ref := range showSeed(t, s, "", seed.ID).References {
			linked = append(linked, ref.Kind+" "+protocol.Deref(ref.Path)+protocol.Deref(ref.Repository)+protocol.Deref(ref.NotebookDocumentID)+protocol.Deref(ref.URL))
		}
		slices.Sort(linked)
		if want := []string{"markdown_file docs/plans/x.mdattn", "notebook nb-7", "url https://example.test/pr/1"}; !slices.Equal(linked, want) {
			t.Errorf("linked references = %q, want %q", linked, want)
		}

		for _, dir := range []string{s.Path(), filepath.Join(s.Dir, "notebook")} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		plan, report, exported := s.Path("plan.bin"), s.Path("report.pdf"), s.Path("exported.pdf")
		for _, path := range []string{plan, report} {
			if err := os.WriteFile(path, []byte("bytes of "+path), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		moved := s.Run(testworld.Invocation{Args: []string{"seed", "attach", seed.ID, "--path", "plan.bin", "--move"}, Dir: s.Path()})
		requireStdout(t, moved, "move "+plan+"\n", "\nmarkdown target ")
		requireLines(t, "copy", seedAs(t, s, "", "attach", seed.ID, "--copy", "--path", report), "copy "+report+"\n")
		if _, err := os.Stat(plan); !os.IsNotExist(err) {
			t.Errorf("the moved source is still there: %v", err)
		}
		if _, err := os.Stat(report); err != nil {
			t.Errorf("the copied source is gone: %v", err)
		}
		detached := s.Run(testworld.Invocation{Args: []string{"seed", "detach", seed.ID, "--path", "report.pdf", "--to", "exported.pdf"}, Dir: s.Path()})
		requireStdout(t, detached, "\nto "+exported+"\n")
		if content, err := os.ReadFile(exported); err != nil || string(content) != "bytes of "+report {
			t.Errorf("the detached file reads %q (%v)", content, err)
		}
		var owned []string
		for _, artifact := range showSeed(t, s, "", seed.ID).Artifacts {
			owned = append(owned, artifact.Filename)
		}
		if !slices.Equal(owned, []string{"plan.bin"}) {
			t.Errorf("the seed owns %q, want only the moved plan", owned)
		}
	})

	t.Run("review show counts candidates as text or JSON", func(t *testing.T) {
		requireStdout(t, s.Attn("seed", "review", "show"), "0 seeds need review\n")
		var review protocol.SeedReviewResult
		s.Attn("seed", "review", "show", "--json").JSON(t, &review)
		if review.Review != nil || review.CandidateCount != 0 {
			t.Errorf("review show --json = %+v", review)
		}
		requireFailure(t, s.Attn("seed", "review", "keep", "r-missing", "--json", "s-7k3f9m"), "seed review keep: ", "no Garden review r-missing exists")
	})
}
