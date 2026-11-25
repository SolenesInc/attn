package daemon_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSeedSearchMatchesWordsAndIdentifiers(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client().WithGardenProfile(app.SelectedProfile(), "")
		ids := map[string]string{}
		for _, row := range []struct{ key, title, body string }{
			{"report", "Publish the report", ""},
			{"callout", "Callout races the app disconnect", ""},
			{"flaky", "A flaky connection", ""},
			{"identifier", "Regression test", "TestARefusedDashboardRefreshKeepsThePreviousList"},
			{"prose", "A refused dashboard refresh keeps the previous list", ""},
		} {
			seed, err := cli.SeedPlant("", row.title, row.body, "", "")
			if err != nil {
				t.Fatal(err)
			}
			ids[row.key] = seed.ID
		}
		for _, tc := range []struct {
			query string
			want  []string
		}{
			{"port", nil},
			{"callouts disconnected", []string{"callout"}},
			{"flak", []string{"flaky"}},
			{"refused dashboard", []string{"identifier", "prose"}},
			{"TestARefusedDashboardRefreshKeepsThePreviousList", []string{"identifier", "prose"}},
		} {
			func() {
				got := gardenSearch(t, cli, "", tc.query, 0)
				if len(got.Hits) != len(tc.want) && !strings.HasPrefix(tc.query, "TestA") {
					t.Fatalf("got %+v, want %v", got, tc.want)
				}
				for _, key := range tc.want {
					found := false
					for _, hit := range got.Hits {
						if hit.Seed.ID == ids[key] {
							found = true
							if key == "identifier" && hit.Snippet != "TestARefusedDashboardRefreshKeepsThePreviousList" {
								t.Errorf("snippet is not the original identifier: %q", hit.Snippet)
							}
						}
					}
					if !found {
						t.Errorf("missing %s in %+v", key, got)
					}
				}
			}()
		}
		for _, tc := range []struct {
			query string
			limit int
			wants []string
		}{
			{"   ", 0, []string{"attn seed search"}},
			{"***", 0, []string{"words"}},
			{strings.Repeat("x", garden.MaxSearchQueryChars+1), 0, []string{"max_query_chars=400", "asked for 401"}},
			{"seed", -1, []string{"asked for -1", "cannot be negative"}},
			{"seed", garden.MaxSearchResults + 1, []string{fmt.Sprintf("max_results=%d", garden.MaxSearchResults), fmt.Sprintf("asked for %d", garden.MaxSearchResults+1)}},
		} {
			_, err := cli.SeedSearch("", tc.query, tc.limit)
			if err == nil {
				t.Fatalf("query %q limit %d was not refused", tc.query, tc.limit)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q lacks %q", err, want)
				}
			}
		}
	})
}

func TestSeedSearchMatchesUnicodeWords(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client().WithGardenProfile(app.SelectedProfile(), "")
		for _, tc := range []struct{ text, query string }{
			{"landmine—decision", "decision"},
			{"say “quoted” here", "quoted"},
			{"step →deploy", "deploy"},
			{"C1–C3", "c3"},
			{"⌘K", "k"},
			{"attn’s", "s"},
			{"Über alles", "über"},
			{"Über alles", "Über"},
			{"ÉlanÜber café 東京42", "élan über"},
			{"İstanbul visible", "istanbul"},
		} {
			seed, err := cli.SeedPlant("", "Unicode regression", tc.text, "", "")
			if err != nil {
				t.Fatal(err)
			}
			result := gardenSearch(t, cli, "", tc.query, garden.MaxSearchResults)
			found := false
			for _, hit := range result.Hits {
				if hit.Seed.ID == seed.ID {
					found = true
					if hit.Snippet != tc.text {
						t.Errorf("query %q: quote %q, want original %q", tc.query, hit.Snippet, tc.text)
					}
				}
			}
			if !found {
				t.Errorf("query %q missed %q: %+v", tc.query, tc.text, result)
			}
		}
	})
}

func TestSeedSearchRanksCoverageAndKeepsClosedSeeds(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client().WithGardenProfile(app.SelectedProfile(), "")
		plant := func(title, body string) string {
			seed, err := cli.SeedPlant("", title, body, "", "")
			if err != nil {
				t.Fatal(err)
			}
			return seed.ID
		}
		title := plant("Quartz amber", "")
		body := plant("Body match", "Original quartz amber text")
		log := plant("Log match", "")
		gardenSearchNote(t, cli, "", log, "A log entry quoting quartz amber together.")
		cross := plant("Quartz across fields", "Amber lives in the body")
		for i := 0; i < 11; i++ {
			plant(fmt.Sprintf("Quartz filler %d", i), "")
		}
		result := gardenSearch(t, cli, "", "quartz amber", 0)
		if result.Matched != 4 || result.Partial != 11 || len(result.Hits) != 10 || result.Limit != 10 || result.Searched != 15 {
			t.Fatalf("counts or default: %+v", result)
		}
		if result.Hits[0].Seed.ID != title {
			t.Fatalf("title must rank first: %+v", result.Hits)
		}
		positions := map[string]int{}
		for i, hit := range result.Hits {
			positions[hit.Seed.ID] = i
			if _, err := time.Parse(time.RFC3339Nano, hit.LastActive); err != nil {
				t.Errorf("last_active %q: %v", hit.LastActive, err)
			}
			if hit.Seed.Status != garden.StatusPlanted || hit.Where == "" || hit.Snippet == "" {
				t.Errorf("incomplete hit: %+v", hit)
			}
			if hit.Seed.ID == body && (hit.Where != garden.MatchBody || hit.Snippet != "Original quartz amber text") {
				t.Errorf("body quote: %+v", hit)
			}
			if hit.Seed.ID == log && (hit.Where != garden.MatchLog || hit.Snippet != "A log entry quoting quartz amber together.") {
				t.Errorf("log quote: %+v", hit)
			}
		}
		for _, id := range []string{body, log, cross} {
			if i, ok := positions[id]; !ok || i >= 4 {
				t.Errorf("full match %s did not precede partial matches: %+v", id, positions)
			}
		}
		raised := gardenSearch(t, cli, "", "quartz amber", garden.MaxSearchResults)
		if len(raised.Hits) != 15 || raised.Matched != 4 || raised.Partial != 11 {
			t.Errorf("raised limit: %+v", raised)
		}

		open := plant("Paired ranking", "")
		w.advance(time.Hour)
		closed := plant("Paired ranking", "")
		if _, err := cli.SeedTransition("", closed, "harvest", "finished", false, client.SeedTransitionOptions{}); err != nil {
			t.Fatal(err)
		}
		paired := gardenSearch(t, cli, "", "paired ranking", 0)
		if len(paired.Hits) != 2 || paired.Hits[0].Seed.ID != open || paired.Hits[1].Seed.ID != closed || paired.Hits[1].Seed.Status != garden.StatusHarvested {
			t.Fatalf("open boost and closed visibility: %+v", paired)
		}
	})
}

func TestSeedSearchFollowsWritesAndRestart(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profile := app.SelectedProfile()
		cli := w.Client().WithGardenProfile(profile, "")
		seed, err := cli.SeedPlant("", "Durable word search", "Obsidian preserves the original body", "", "")
		if err != nil {
			t.Fatal(err)
		}
		id := seed.ID
		gardenSearchNote(t, cli, "", id, "Chrysoprase is only in this note")
		assertHit := func(query string, want bool) {
			t.Helper()
			result := gardenSearch(t, cli, "", query, 0)
			if (len(result.Hits) == 1 && result.Hits[0].Seed.ID == id) != want || !want && len(result.Hits) != 0 {
				t.Fatalf("search %q want found=%v: %+v", query, want, result)
			}
		}
		assertHit("chrysoprase", true)
		if _, err := cli.SeedEdit(id, "Malachite replaces the previous body", garden.BodySHA256(seed.Body), ""); err != nil {
			t.Fatal(err)
		}
		assertHit("malachite", true)
		assertHit("obsidian", false)
		if _, err := cli.SeedRollback(id, nil); err != nil {
			t.Fatal(err)
		}
		assertHit("obsidian", true)
		assertHit("malachite", false)
		if _, err := cli.SeedTransition("", id, "harvest", "finished", false, client.SeedTransitionOptions{}); err != nil {
			t.Fatal(err)
		}
		before := gardenSearch(t, cli, "", "obsidian chrysoprase", 0)
		if before.Matched != 1 || before.Hits[0].Seed.Status != garden.StatusHarvested {
			t.Fatalf("closed full match: %+v", before)
		}
		w.restart()
		cli = w.Client().WithGardenProfile(profile, "")
		after := gardenSearch(t, cli, "", "obsidian chrysoprase", 0)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("restart changed search: before=%+v after=%+v", before, after)
		}
	})
}

func gardenSearchNote(t *testing.T, cli *client.Client, session, seedID, body string) {
	t.Helper()
	if _, err := cli.SeedNote(protocol.SessionID(session), seedID, body, "", false, nil); err != nil {
		t.Fatalf("note %q on %s: %v", body, seedID, err)
	}
}

func gardenSearch(t *testing.T, cli *client.Client, session, query string, limit int) *protocol.SeedSearchResult {
	t.Helper()
	result, err := cli.SeedSearch(protocol.SessionID(session), query, limit)
	if err != nil {
		t.Fatalf("search %q: %v", query, err)
	}
	return result
}

func TestGardenSearchWSFiltersSnippetsAndPaging(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client().WithGardenProfile(app.SelectedProfile(), "")
		registerSessions(t, w, cli, "hazel")
		plot, err := cli.SeedPlant("", "Migration plot", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		openChild, err := cli.SeedPlant("", "Wire the garden search", "This garden body mentions quasar", plot.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		gardenSearchNote(t, cli, "", openChild.ID, "Open note mentions beryllium.")
		closedChild, err := cli.SeedPlant("", "Shipped sidebar bar work", "shipped body", plot.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		gardenSearchNote(t, cli, "", closedChild.ID, "Note mentions zirconium token in closed history.")
		lifeMove(t, cli, "", closedChild.ID, "harvest", "shipped", "")
		outsideSeed, err := cli.SeedPlant("", "Outside garden task", "unrelated text", "", "")
		if err != nil {
			t.Fatal(err)
		}
		lifeMove(t, cli, "hazel", outsideSeed.ID, "tend", "", "")

		search := func(req protocol.GardenSearchMessage) protocol.GardenSearchResultMessage {
			t.Helper()
			req.Cmd = protocol.CmdGardenSearch
			res := testworld.Request(app, req, protocol.EventGardenSearchResult,
				func(m protocol.GardenSearchResultMessage) bool { return m.RequestID == req.RequestID })
			if !res.Success {
				t.Fatalf("garden_search %s failed: %s", req.RequestID, protocol.Deref(res.Error))
			}
			return res
		}

		scoped := search(protocol.GardenSearchMessage{
			RequestID: "s-plot", Terms: []string{"garden"}, PlotID: protocol.Ptr(plot.ID),
		})
		if len(scoped.Hits) != 1 || scoped.Hits[0].Seed.ID != openChild.ID || scoped.Outside != 1 || scoped.InPlot != 0 {
			t.Fatalf("scoped plot search = %+v, want hit=%s outside=1", scoped, openChild.ID)
		}
		if protocol.Deref(scoped.Hits[0].PlotTitle) != "Migration plot" {
			t.Errorf("plot_title = %q, want Migration plot", protocol.Deref(scoped.Hits[0].PlotTitle))
		}
		if scoped.Hits[0].Seed.Body != "" {
			t.Errorf("search hit unexpectedly carried body %q", scoped.Hits[0].Seed.Body)
		}

		widened := search(protocol.GardenSearchMessage{
			RequestID: "s-wide", Terms: []string{"garden"}, PlotID: protocol.Ptr(plot.ID), Wide: protocol.Ptr(true), Limit: protocol.Ptr(1),
		})
		if widened.Total != 2 || len(widened.Hits) != 1 || widened.InPlot != 1 || protocol.Deref(widened.NextOffset) != 1 {
			t.Fatalf("widened page 1 = %+v, want total=2 in_plot=1 next_offset=1", widened)
		}
		page2 := search(protocol.GardenSearchMessage{
			RequestID: "s-wide-2", Terms: []string{"garden"}, PlotID: protocol.Ptr(plot.ID), Wide: protocol.Ptr(true), Offset: widened.NextOffset, Limit: protocol.Ptr(1),
		})
		if len(page2.Hits) != 1 || page2.NextOffset != nil || page2.Hits[0].Seed.ID == widened.Hits[0].Seed.ID {
			t.Fatalf("widened page 2 = %+v, want second distinct hit", page2)
		}

		hidden := search(protocol.GardenSearchMessage{RequestID: "s-log-open", Terms: []string{"zirconium"}})
		if len(hidden.Hits) != 0 || hidden.HiddenClosed != 1 {
			t.Errorf("open-only log search = %+v, want 0 hits and hidden_closed=1", hidden)
		}

		withClosed := search(protocol.GardenSearchMessage{RequestID: "s-log-any", Terms: []string{"zirconium"}, Is: []string{"any"}})
		if len(withClosed.Hits) != 1 || withClosed.Hits[0].Seed.ID != closedChild.ID || withClosed.Hits[0].Where != garden.MatchLog {
			t.Fatalf("closed log search = %+v, want log hit on %s", withClosed, closedChild.ID)
		}
		snippet := protocol.Deref(withClosed.Hits[0].Snippet)
		if len(withClosed.Hits[0].SnippetRanges) != 1 {
			t.Fatalf("log hit snippet=%q ranges=%+v, want 1 range", snippet, withClosed.Hits[0].SnippetRanges)
		}
		sr := withClosed.Hits[0].SnippetRanges[0]
		if sr.Start < 0 || sr.End > len(snippet) || snippet[sr.Start:sr.End] != "zirconium" {
			t.Errorf("log snippet range %+v slices %q, want \"zirconium\"", sr, snippet)
		}

		byID := search(protocol.GardenSearchMessage{RequestID: "s-id", Terms: []string{closedChild.ID}, Is: []string{"any"}})
		if len(byID.Hits) != 2 || byID.Hits[0].Seed.ID != closedChild.ID || !byID.Hits[0].IDHit || byID.Hits[0].Where != "id" {
			t.Fatalf("id search = %+v, want id hit first on %s and log mention on plot %s", byID, closedChild.ID, plot.ID)
		}
		if len(byID.Hits[0].TitleRanges) != 0 {
			t.Errorf("id search on title starting with s produced bogus title_ranges %+v", byID.Hits[0].TitleRanges)
		}
		if byID.Hits[1].Seed.ID != plot.ID || byID.Hits[1].Where != garden.MatchLog || len(byID.Hits[1].SnippetRanges) != 1 {
			t.Fatalf("id mention hit = %+v, want log mention on plot %s", byID.Hits[1], plot.ID)
		}
		plotSnippet := protocol.Deref(byID.Hits[1].Snippet)
		psr := byID.Hits[1].SnippetRanges[0]
		if plotSnippet[psr.Start:psr.End] != closedChild.ID {
			t.Errorf("id mention snippet range %+v slices %q, want %q", psr, plotSnippet, closedChild.ID)
		}

		closedOnlyOpenTerm := search(protocol.GardenSearchMessage{RequestID: "s-closed-open", Terms: []string{"garden"}, Is: []string{"closed"}})
		if len(closedOnlyOpenTerm.Hits) != 0 {
			t.Errorf("is:closed matched open seeds: %+v", closedOnlyOpenTerm.Hits)
		}

		substr := search(protocol.GardenSearchMessage{RequestID: "s-sub", Terms: []string{"side", "bar"}, Is: []string{"closed"}})
		if len(substr.Hits) != 1 || substr.Hits[0].Seed.ID != closedChild.ID {
			t.Fatalf("substring title search = %+v, want only %s", substr, closedChild.ID)
		}
		title := substr.Hits[0].Seed.Title
		if len(substr.Hits[0].TitleRanges) != 2 ||
			title[substr.Hits[0].TitleRanges[0].Start:substr.Hits[0].TitleRanges[0].End] != "sidebar" ||
			title[substr.Hits[0].TitleRanges[1].Start:substr.Hits[0].TitleRanges[1].End] != "bar" {
			t.Errorf("merged title_ranges = %+v on %q, want [sidebar bar]", substr.Hits[0].TitleRanges, title)
		}

		titleAndLog := search(protocol.GardenSearchMessage{RequestID: "s-title-log", Terms: []string{"garden", "beryllium"}})
		if len(titleAndLog.Hits) != 1 || titleAndLog.Hits[0].Seed.ID != openChild.ID || titleAndLog.Hits[0].Where != garden.MatchLog {
			t.Fatalf("title+log search when body repeats title word = %+v, want where=log on %s", titleAndLog, openChild.ID)
		}
		if !strings.Contains(protocol.Deref(titleAndLog.Hits[0].Snippet), "beryllium") {
			t.Errorf("title+log snippet = %q, want beryllium note", protocol.Deref(titleAndLog.Hits[0].Snippet))
		}

		titleAndTender := search(protocol.GardenSearchMessage{RequestID: "s-title-tender", Terms: []string{"outside", "hazel"}})
		if len(titleAndTender.Hits) != 1 || titleAndTender.Hits[0].Seed.ID != outsideSeed.ID {
			t.Fatalf("title+tender search = %+v, want %s", titleAndTender, outsideSeed.ID)
		}
		if titleAndTender.Hits[0].Where == garden.MatchBody || titleAndTender.Hits[0].Snippet != nil {
			t.Errorf("title+tender hit mislabeled where=%q snippet=%v, want non-body with no snippet", titleAndTender.Hits[0].Where, titleAndTender.Hits[0].Snippet)
		}
	})
}

func TestGardenSearchFindsAnExactAutomationSeedIDFromAll(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	cli := w.Client().WithGardenProfile(app.SelectedProfile(), "")
	setSetting(t, app, "garden_v2_enabled", "true")

	var automation protocol.AutomationDefinitionSummary
	automationUnreachableFolder(t, w, "check", func() {
		automation = applyAutomation(t, cli, manualAutomation(w, "Check locally."))
	})
	if _, err := cli.AutomationRun(automation.ID, "first", ""); err == nil {
		t.Fatal("an automation with a missing working directory succeeded")
	}
	runs := automationRuns(t, cli, automation.ID)
	if len(runs) != 1 || runs[0].SeedID == nil {
		t.Fatalf("automation runs = %+v, want one run with a seed", runs)
	}
	id := *runs[0].SeedID

	for _, tc := range []struct {
		name  string
		term  string
		beds  []string
		lens  []string
		found bool
	}{
		{name: "full ID from All", term: id, lens: []string{"any"}, found: true},
		{name: "partial ID from All", term: id[:len(id)-1], lens: []string{"any"}},
		{name: "explicit automation bed", term: id, beds: []string{"automations"}, lens: []string{"any"}, found: true},
		{name: "explicit unassigned bed", term: id, beds: []string{"unassigned"}, lens: []string{"any"}},
		{name: "open lens", term: id, lens: []string{"open"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := testworld.Request(app, protocol.GardenSearchMessage{
				Cmd: protocol.CmdGardenSearch, RequestID: tc.name, BedScope: protocol.Ptr("all"),
				Terms: []string{tc.term}, Beds: tc.beds, Is: tc.lens,
			}, protocol.EventGardenSearchResult, func(m protocol.GardenSearchResultMessage) bool { return m.RequestID == tc.name })
			if !result.Success {
				t.Fatalf("search failed: %s", protocol.Deref(result.Error))
			}
			if tc.found {
				if len(result.Hits) != 1 || result.Hits[0].Seed.ID != id || !result.Hits[0].IDHit {
					t.Errorf("search hits = %+v, want exact ID hit for %s", result.Hits, id)
				}
			} else if len(result.Hits) != 0 {
				t.Errorf("search hits = %+v, want no hits", result.Hits)
			}
		})
	}
}
