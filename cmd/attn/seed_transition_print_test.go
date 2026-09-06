package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

// A closed plot over open work must never close silently: the harvest that
// strands growing children says so on the same screen that confirmed the move.
func TestFprintTransitionWarnsOnClosingAPlotWithOpenChildren(t *testing.T) {
	var buf bytes.Buffer
	fprintTransition(&buf, &protocol.SeedTransitionResult{
		Seed: protocol.Seed{
			ID: "s-7k3f9m", Status: "harvested",
			PlotProgress: &protocol.SeedPlotProgress{Total: 3, Done: 1, Withered: 1, Growing: 1},
		},
	})
	out := buf.String()
	if !strings.Contains(out, "s-7k3f9m is harvested") {
		t.Fatalf("the move itself is not confirmed:\n%s", out)
	}
	if !strings.Contains(out, "1 open seed") {
		t.Fatalf("closing a plot with open children says nothing about them:\n%s", out)
	}
}

func TestFprintTransitionStaysQuietWhenNothingIsStranded(t *testing.T) {
	cases := []struct {
		name string
		seed protocol.Seed
	}{
		{"childless harvest", protocol.Seed{ID: "s-aaaaaa", Status: "harvested"}},
		{"open plot keeps growing", protocol.Seed{
			ID: "s-bbbbbb", Status: "growing",
			PlotProgress: &protocol.SeedPlotProgress{Total: 2, Growing: 2},
		}},
		{"plot closed after its children", protocol.Seed{
			ID: "s-cccccc", Status: "harvested",
			PlotProgress: &protocol.SeedPlotProgress{Total: 2, Done: 1, Withered: 1},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			fprintTransition(&buf, &protocol.SeedTransitionResult{Seed: tc.seed})
			if strings.Contains(buf.String(), "open seed") {
				t.Fatalf("warned with nothing stranded:\n%s", buf.String())
			}
		})
	}
}

// Closing a blocker readies its dependents silently; the harvest that did it is
// the one moment the next move is worth naming.
func TestFprintTransitionNamesWhatTheCloseFreed(t *testing.T) {
	var buf bytes.Buffer
	fprintTransition(&buf, &protocol.SeedTransitionResult{
		Seed: protocol.Seed{ID: "s-7k3f9m", StepSlug: "lay-the-pipe", Status: "harvested"},
		Unblocked: []protocol.Seed{
			{ID: "s-2p4qxv", StepSlug: "run-water-through", Title: "Run water through it"},
			{ID: "s-8h1kdd", StepSlug: "paint-the-wall", Title: "Paint the wall", TenderMember: "trellis"},
		},
	})
	out := buf.String()
	if !strings.Contains(out, "this unblocked 2 seed(s):") {
		t.Fatalf("the ripple is not announced:\n%s", out)
	}
	for _, want := range []string{"s-2p4qxv", "Run water through it", "s-8h1kdd", "Paint the wall"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the freed seeds do not name %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "tended by Trellis") {
		t.Fatalf("a freed seed somebody already holds reads as free:\n%s", out)
	}
	if !strings.Contains(out, "`attn seed tend <id>` claims one") {
		t.Fatalf("a freed seed nobody holds says nothing about picking it up:\n%s", out)
	}
}

func TestFprintTransitionIsQuietAboutARippleThatFreedNobody(t *testing.T) {
	var buf bytes.Buffer
	fprintTransition(&buf, &protocol.SeedTransitionResult{
		Seed: protocol.Seed{ID: "s-7k3f9m", Status: "harvested"},
	})
	if strings.Contains(buf.String(), "unblocked") {
		t.Fatalf("a close that freed nothing still talked about it:\n%s", buf.String())
	}
}

// Every freed seed being held makes `tend` the wrong next move to suggest.
func TestFprintTransitionOffersNoClaimWhenEveryFreedSeedIsHeld(t *testing.T) {
	var buf bytes.Buffer
	fprintTransition(&buf, &protocol.SeedTransitionResult{
		Seed:      protocol.Seed{ID: "s-7k3f9m", Status: "withered"},
		Unblocked: []protocol.Seed{{ID: "s-2p4qxv", Title: "Run water through it", TenderSession: "sess-c"}},
	})
	out := buf.String()
	if !strings.Contains(out, "this unblocked 1 seed(s):") {
		t.Fatalf("wither says nothing about what it freed:\n%s", out)
	}
	if strings.Contains(out, "claims one") {
		t.Fatalf("offered a claim on a seed somebody holds:\n%s", out)
	}
}

func TestFprintTransitionSaysWhatTheSeedWaitsOn(t *testing.T) {
	var buf bytes.Buffer
	fprintTransition(&buf, &protocol.SeedTransitionResult{
		Seed: protocol.Seed{
			ID: "s-7k3f9m", StepSlug: "harvest-on-merge", Status: "dormant",
			HarvestWhen: &protocol.SeedHarvestCondition{
				PullRequest: "github.com:victorarias/attn#118",
				URL:         "https://github.com/victorarias/attn/pull/118",
				SetAt:       "2026-09-02T00:00:00Z",
			},
		},
	})
	out := buf.String()
	if !strings.Contains(out, "s-7k3f9m (harvest-on-merge) is dormant") {
		t.Fatalf("the move itself is not confirmed:\n%s", out)
	}
	if !strings.Contains(out, "harvests when victorarias/attn#118 merges") {
		t.Fatalf("an armed seed never says what it waits on:\n%s", out)
	}
}

func TestFprintTransitionConfirmsAClearedCondition(t *testing.T) {
	var buf bytes.Buffer
	fprintTransition(&buf, &protocol.SeedTransitionResult{
		Seed: protocol.Seed{ID: "s-7k3f9m", Status: "growing"},
	}, true)
	if !strings.Contains(buf.String(), "harvest-on-merge cleared") {
		t.Fatalf("clearing the condition said nothing:\n%s", buf.String())
	}
}

func TestFprintSeedShowsTheHarvestCondition(t *testing.T) {
	var buf bytes.Buffer
	fprintSeed(&buf, protocol.Seed{
		ID: "s-7k3f9m", Status: "dormant",
		HarvestWhen: &protocol.SeedHarvestCondition{
			PullRequest: "github.com:victorarias/attn#118",
			URL:         "https://github.com/victorarias/attn/pull/118",
		},
	})
	if !strings.Contains(buf.String(), "harvests when") || !strings.Contains(buf.String(), "victorarias/attn#118 merges") {
		t.Fatalf("show never says the seed is armed:\n%s", buf.String())
	}
}

func TestHarvestWhenSuffixIsShortAndOnlyForArmedSeeds(t *testing.T) {
	if got := harvestWhenSuffix(protocol.Seed{ID: "s-7k3f9m"}); got != "" {
		t.Fatalf("an unarmed seed carries a suffix: %q", got)
	}
	got := harvestWhenSuffix(protocol.Seed{HarvestWhen: &protocol.SeedHarvestCondition{
		PullRequest: "github.com:victorarias/attn#118",
	}})
	if got != "  [harvests when victorarias/attn#118 merges]" {
		t.Fatalf("row suffix = %q", got)
	}
}

func TestHarvestWhenArgsReadsTheOptionalURL(t *testing.T) {
	cases := []struct {
		name    string
		verb    string
		args    []string
		seedID  string
		opts    client.SeedTransitionOptions
		refusal string
	}{
		{
			name: "a plain harvest arms nothing",
			verb: "harvest", args: []string{"s-7k3f9m", "-m", "done"}, seedID: "s-7k3f9m",
		},
		{
			name: "arming infers the pull request",
			verb: "harvest", args: []string{"s-7k3f9m", "--when-merged"}, seedID: "s-7k3f9m",
			opts: client.SeedTransitionOptions{WhenMerged: true},
		},
		{
			name: "arming takes the url after the id",
			verb: "harvest", args: []string{"s-7k3f9m", "--when-merged", "https://github.com/victorarias/attn/pull/118"},
			seedID: "s-7k3f9m",
			opts:   client.SeedTransitionOptions{WhenMerged: true, PullRequestURL: "https://github.com/victorarias/attn/pull/118"},
		},
		{
			name: "arming takes the url before the id",
			verb: "harvest", args: []string{"--when-merged", "s-7k3f9m", "https://github.com/victorarias/attn/pull/118"},
			seedID: "s-7k3f9m",
			opts:   client.SeedTransitionOptions{WhenMerged: true, PullRequestURL: "https://github.com/victorarias/attn/pull/118"},
		},
		{
			name: "clearing disarms without a reason",
			verb: "harvest", args: []string{"s-7k3f9m", "--when-merged", "--clear"}, seedID: "s-7k3f9m",
			opts: client.SeedTransitionOptions{ClearHarvestWhen: true},
		},
		{
			name: "a reason belongs to the merge",
			verb: "harvest", args: []string{"s-7k3f9m", "--when-merged", "-m", "done"},
			refusal: "takes no -m",
		},
		{
			name: "a second positional must be a pull request url",
			verb: "harvest", args: []string{"s-7k3f9m", "--when-merged", "s-2p4qxv"},
			refusal: "is not a pull request url",
		},
		{
			name: "clear alone says the whole form",
			verb: "harvest", args: []string{"s-7k3f9m", "--clear"},
			refusal: "--when-merged --clear",
		},
		{
			name: "only harvest arms",
			verb: "park", args: []string{"s-7k3f9m", "--when-merged"},
			refusal: "belongs to harvest",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSeedFlags(tc.verb)
			seedID, opts, err := harvestWhenArgs(tc.verb, f, f.parse(tc.verb, tc.args))
			if tc.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refusal) {
					t.Fatalf("refusal = %v, want text %q", err, tc.refusal)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if seedID != tc.seedID {
				t.Fatalf("seed id = %q, want %q", seedID, tc.seedID)
			}
			if opts != tc.opts {
				t.Fatalf("options = %+v, want %+v", opts, tc.opts)
			}
		})
	}
}
