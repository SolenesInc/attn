package garden

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

const (
	me    = "sess-me"
	other = "sess-you"
)

func alive(protocol.SessionID) bool { return true }

type testStanding struct{ live func(protocol.SessionID) bool }

func (s testStanding) Holds(p who.Party) bool {
	if id, ok := p.Session(); ok {
		return s.live(id)
	}
	return true
}
func (testStanding) Oversees(who.Actor, who.Party) bool { return false }
func (testStanding) Name(p who.Party) string            { return p.String() }

func gone(protocol.SessionID) bool { return false }

func session(id protocol.SessionID) who.Party { return who.Session(id) }

func member(key string) who.Party {
	k, err := who.ParseMemberKey(key)
	if err != nil {
		panic(err)
	}
	return who.Member(k)
}

func seedIn(status string, tender who.Party) Seed {
	return Seed{ID: "s-7k3f9m", Title: "a seed", Status: status, Tender: tender}
}

func claimingAs(p who.Party) Ask { return Ask{Actor: p.Actor(), Claimant: p} }

func TestTransitionMatrix(t *testing.T) {
	held := session(other)
	mine := session(me)

	cases := []struct {
		name   string
		seed   Seed
		verb   Verb
		want   string
		refuse string
		force  bool
	}{
		{name: "planted/tend", seed: seedIn(StatusPlanted, who.Party{}), verb: VerbTend, want: StatusGrowing},
		{name: "planted/park", seed: seedIn(StatusPlanted, who.Party{}), verb: VerbPark, want: StatusDormant},
		{name: "planted/harvest", seed: seedIn(StatusPlanted, who.Party{}), verb: VerbHarvest, want: StatusHarvested},
		{name: "planted/wither", seed: seedIn(StatusPlanted, who.Party{}), verb: VerbWither, want: StatusWithered},
		{name: "planted/replant", seed: seedIn(StatusPlanted, who.Party{}), verb: VerbReplant, want: StatusPlanted},

		{name: "growing by me/tend", seed: seedIn(StatusGrowing, mine), verb: VerbTend, want: StatusGrowing},
		{name: "growing by me/park", seed: seedIn(StatusGrowing, mine), verb: VerbPark, want: StatusDormant},
		{name: "growing by me/harvest", seed: seedIn(StatusGrowing, mine), verb: VerbHarvest, want: StatusHarvested},
		{name: "growing by me/wither", seed: seedIn(StatusGrowing, mine), verb: VerbWither, want: StatusWithered},
		{name: "growing by me/replant", seed: seedIn(StatusGrowing, mine), verb: VerbReplant, want: StatusPlanted},

		{name: "growing by another/tend", seed: seedIn(StatusGrowing, held), verb: VerbTend, refuse: "--force acts anyway"},
		{name: "growing by another/park", seed: seedIn(StatusGrowing, held), verb: VerbPark, refuse: "--force acts anyway"},
		{name: "growing by another/harvest", seed: seedIn(StatusGrowing, held), verb: VerbHarvest, refuse: "--force acts anyway"},
		{name: "growing by another/wither", seed: seedIn(StatusGrowing, held), verb: VerbWither, refuse: "--force acts anyway"},
		{name: "growing by another/replant", seed: seedIn(StatusGrowing, held), verb: VerbReplant, refuse: "--force acts anyway"},

		{name: "growing by another/tend names them", seed: seedIn(StatusGrowing, held), verb: VerbTend, refuse: "tended by session:" + other},

		{name: "forced/tend", seed: seedIn(StatusGrowing, held), verb: VerbTend, force: true, want: StatusGrowing},
		{name: "forced/park", seed: seedIn(StatusGrowing, held), verb: VerbPark, force: true, want: StatusDormant},
		{name: "forced/harvest", seed: seedIn(StatusGrowing, held), verb: VerbHarvest, force: true, want: StatusHarvested},
		{name: "forced/wither", seed: seedIn(StatusGrowing, held), verb: VerbWither, force: true, want: StatusWithered},
		{name: "forced/replant", seed: seedIn(StatusGrowing, held), verb: VerbReplant, force: true, want: StatusPlanted},

		{name: "force with nobody to take from", seed: seedIn(StatusPlanted, who.Party{}), verb: VerbPark, force: true, want: StatusDormant},

		{name: "dormant/tend", seed: seedIn(StatusDormant, who.Party{}), verb: VerbTend, want: StatusGrowing},
		{name: "dormant/park", seed: seedIn(StatusDormant, who.Party{}), verb: VerbPark, want: StatusDormant},
		{name: "dormant/harvest", seed: seedIn(StatusDormant, who.Party{}), verb: VerbHarvest, want: StatusHarvested},
		{name: "dormant/wither", seed: seedIn(StatusDormant, who.Party{}), verb: VerbWither, want: StatusWithered},
		{name: "dormant/replant", seed: seedIn(StatusDormant, who.Party{}), verb: VerbReplant, want: StatusPlanted},

		{name: "harvested/tend", seed: seedIn(StatusHarvested, who.Party{}), verb: VerbTend, refuse: "reopens before it moves"},
		{name: "harvested/park", seed: seedIn(StatusHarvested, who.Party{}), verb: VerbPark, refuse: "reopens before it moves"},
		{name: "harvested/harvest", seed: seedIn(StatusHarvested, who.Party{}), verb: VerbHarvest, want: StatusHarvested},
		{name: "harvested/wither", seed: seedIn(StatusHarvested, who.Party{}), verb: VerbWither, refuse: "reopens before it moves"},
		{name: "harvested/replant", seed: seedIn(StatusHarvested, who.Party{}), verb: VerbReplant, want: StatusPlanted},

		{name: "withered/tend", seed: seedIn(StatusWithered, who.Party{}), verb: VerbTend, refuse: "reopens before it moves"},
		{name: "withered/park", seed: seedIn(StatusWithered, who.Party{}), verb: VerbPark, refuse: "reopens before it moves"},
		{name: "withered/harvest", seed: seedIn(StatusWithered, who.Party{}), verb: VerbHarvest, refuse: "reopens before it moves"},
		{name: "withered/wither", seed: seedIn(StatusWithered, who.Party{}), verb: VerbWither, want: StatusWithered},
		{name: "withered/replant", seed: seedIn(StatusWithered, who.Party{}), verb: VerbReplant, want: StatusPlanted},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.seed
			reason := ""
			if tc.verb == VerbHarvest || tc.verb == VerbWither {
				reason = "because"
			}
			ask := claimingAs(mine)
			ask.Reason, ask.Force = reason, tc.force
			outcome, err := Transition(tc.seed, tc.verb, ask, testStanding{alive}, nil)
			next := outcome.Seed
			if !reflect.DeepEqual(tc.seed, before) {
				t.Fatalf("the input seed was mutated: %+v", tc.seed)
			}
			if tc.refuse != "" {
				if err == nil {
					t.Fatalf("%s from %s was allowed, want a refusal", tc.verb, tc.seed.Status)
				}
				if !strings.Contains(err.Error(), tc.refuse) {
					t.Fatalf("refusal = %q, want it to say %q", err, tc.refuse)
				}
				if !strings.Contains(err.Error(), tc.seed.ID) {
					t.Fatalf("refusal does not name the seed: %s", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s from %s: %v", tc.verb, tc.seed.Status, err)
			}
			if next.Status != tc.want {
				t.Fatalf("%s from %s landed in %q, want %q", tc.verb, tc.seed.Status, next.Status, tc.want)
			}
		})
	}
}

func TestTransitionClaimsAndReasons(t *testing.T) {
	mine := session(me)
	held := member("alder")
	armed := func(seed Seed) Seed {
		seed.HarvestWhen = &HarvestCondition{PullRequest: "github.com:victorarias/attn#113", URL: "https://github.com/victorarias/attn/pull/113", SetAt: "2026-09-02T00:21:00Z"}
		return seed
	}
	withReason := func(seed Seed, reason string) Seed {
		seed.Reason = reason
		return seed
	}
	as := func(p who.Party, reason string) Ask {
		ask := claimingAs(p)
		ask.Reason = reason
		return ask
	}
	cases := []struct {
		name          string
		seed          Seed
		verb          Verb
		ask           Ask
		sessionGone   bool
		refuse        []string
		tender        who.Party
		reason        string
		keepCondition bool
	}{
		{name: "tend records the tender", seed: seedIn(StatusPlanted, who.Party{}), verb: VerbTend, ask: as(mine, ""), tender: mine},
		{name: "park releases the claim", seed: seedIn(StatusGrowing, mine), verb: VerbPark, ask: as(mine, "")},
		{name: "harvest releases the claim and keeps its reason", seed: seedIn(StatusGrowing, mine), verb: VerbHarvest, ask: as(mine, "  done  "), reason: "done"},
		{name: "wither releases the claim", seed: seedIn(StatusGrowing, mine), verb: VerbWither, ask: as(mine, "done"), reason: "done"},
		{name: "a wordless wither is fine", seed: seedIn(StatusPlanted, who.Party{}), verb: VerbWither, ask: as(mine, "")},
		{name: "a wordless harvest is refused", seed: seedIn(StatusPlanted, who.Party{}), verb: VerbHarvest, ask: as(mine, "  "), refuse: []string{"-m"}},
		{name: "tend drops no reason silently", seed: seedIn(StatusPlanted, who.Party{}), verb: VerbTend, ask: as(mine, "some words"), refuse: []string{"tend", "attn seed note", "s-7k3f9m"}},
		{name: "park drops no reason silently", seed: seedIn(StatusPlanted, who.Party{}), verb: VerbPark, ask: as(mine, "some words"), refuse: []string{"park", "attn seed note", "s-7k3f9m"}},
		{name: "replant drops no reason silently", seed: seedIn(StatusHarvested, who.Party{}), verb: VerbReplant, ask: as(mine, "some words"), refuse: []string{"replant", "attn seed note", "s-7k3f9m"}},
		{name: "replant clears the closing reason", seed: withReason(seedIn(StatusHarvested, who.Party{}), "shipped it"), verb: VerbReplant, ask: as(mine, "")},
		{name: "a reason past the limit names both numbers and the log", seed: seedIn(StatusPlanted, who.Party{}), verb: VerbHarvest, ask: as(mine, strings.Repeat("x", MaxReasonChars+1)), refuse: []string{"401", "400", "attn seed note"}},
		{name: "the reason limit counts characters, not bytes", seed: seedIn(StatusPlanted, who.Party{}), verb: VerbHarvest, ask: as(mine, strings.Repeat("🌱", MaxReasonChars)), reason: strings.Repeat("🌱", MaxReasonChars)},
		{name: "one character past the limit is refused", seed: seedIn(StatusPlanted, who.Party{}), verb: VerbHarvest, ask: as(mine, strings.Repeat("🌱", MaxReasonChars+1)), refuse: []string{"401 characters"}},
		{name: "a tend that names nobody asks for a member", seed: seedIn(StatusPlanted, who.Party{}), verb: VerbTend, ask: Ask{Actor: who.User()}, refuse: []string{"--for <name>"}},
		{name: "a live claim names its tender and the way forward", seed: seedIn(StatusGrowing, held), verb: VerbTend, ask: as(mine, ""), refuse: []string{"s-7k3f9m", "member:alder", "--force"}},
		{name: "a live session claim names its session", seed: seedIn(StatusGrowing, session(other)), verb: VerbTend, ask: as(mine, ""), refuse: []string{other}},
		{name: "a member claim refuses another member", seed: seedIn(StatusGrowing, member("trellis")), verb: VerbTend, ask: as(member("alder"), ""), refuse: []string{"member:trellis"}},
		{name: "a member claim lets its member back in", seed: seedIn(StatusGrowing, member("trellis")), verb: VerbTend, ask: as(member("trellis"), ""), tender: member("trellis")},
		{name: "the user assigns an unclaimed seed to a member", seed: seedIn(StatusPlanted, who.Party{}), verb: VerbTend, ask: Ask{Actor: who.User(), Claimant: member("trellis")}, tender: member("trellis")},
		{name: "the user reassigns a member's seed to the same member", seed: seedIn(StatusGrowing, member("trellis")), verb: VerbTend, ask: Ask{Actor: who.User(), Claimant: member("trellis")}, tender: member("trellis")},
		{name: "the user cannot assign away a live claim", seed: seedIn(StatusGrowing, member("trellis")), verb: VerbTend, ask: Ask{Actor: who.User(), Claimant: member("alder")}, refuse: []string{"member:trellis", "--force"}},
		{name: "the user cannot park a live claim", seed: seedIn(StatusGrowing, session(other)), verb: VerbPark, ask: Ask{Actor: who.User()}, refuse: []string{other, "--force"}},
		{name: "a member claim moves by its member", seed: seedIn(StatusGrowing, member("trellis")), verb: VerbPark, ask: as(member("trellis"), "")},
		{name: "a claim whose session ended passes to the next tender", seed: seedIn(StatusGrowing, session(other)), verb: VerbTend, ask: as(mine, ""), sessionGone: true, tender: mine},
		{name: "a claim whose session ended can be parked", seed: seedIn(StatusGrowing, session(other)), verb: VerbPark, ask: as(mine, ""), sessionGone: true},
		{name: "a claim whose session ended can be harvested", seed: seedIn(StatusGrowing, session(other)), verb: VerbHarvest, ask: as(mine, "done"), sessionGone: true, reason: "done"},
		{name: "a claim whose session ended can be withered", seed: seedIn(StatusGrowing, session(other)), verb: VerbWither, ask: as(mine, "done"), sessionGone: true, reason: "done"},
		{name: "a claim whose session ended can be replanted", seed: seedIn(StatusGrowing, session(other)), verb: VerbReplant, ask: as(mine, ""), sessionGone: true},
		{name: "a member claim does not end with sessions", seed: seedIn(StatusGrowing, member("trellis")), verb: VerbTend, ask: as(member("alder"), ""), sessionGone: true, refuse: []string{"member:trellis"}},
		{name: "harvest drops the harvest condition", seed: armed(seedIn(StatusPlanted, who.Party{})), verb: VerbHarvest, ask: as(mine, "the pull request landed"), reason: "the pull request landed"},
		{name: "wither drops the harvest condition", seed: armed(seedIn(StatusPlanted, who.Party{})), verb: VerbWither, ask: as(mine, "")},
		{name: "park keeps the harvest condition", seed: armed(seedIn(StatusPlanted, who.Party{})), verb: VerbPark, ask: as(mine, ""), keepCondition: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live := alive
			if tc.sessionGone {
				live = gone
			}
			outcome, err := Transition(tc.seed, tc.verb, tc.ask, testStanding{live}, nil)
			if tc.refuse != nil {
				if err == nil {
					t.Fatalf("%s was allowed, want a refusal", tc.verb)
				}
				for _, want := range tc.refuse {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("refusal %q does not say %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("%s: %v", tc.verb, err)
			}
			next := outcome.Seed
			if next.Tender != tc.tender {
				t.Errorf("tender = %v, want %v", next.Tender, tc.tender)
			}
			if next.Reason != tc.reason {
				t.Errorf("reason = %q, want %q", next.Reason, tc.reason)
			}
			if kept := next.HarvestWhen != nil; kept != tc.keepCondition {
				t.Errorf("harvest condition kept = %v, want %v", kept, tc.keepCondition)
			}
		})
	}
}

func TestTrimReason(t *testing.T) {
	for _, reason := range []string{
		"  PR #71 merged  ",
		"PR #71 merged: " + strings.Repeat("x", MaxReasonChars),
		strings.Repeat("🌱", MaxReasonChars*2),
		strings.Repeat("🌱", MaxReasonChars),
	} {
		trimmed := TrimReason(reason)
		fits := utf8.RuneCountInString(strings.TrimSpace(reason)) <= MaxReasonChars
		if fits && trimmed != strings.TrimSpace(reason) {
			t.Errorf("a reason that fits came back as %q", trimmed)
		}
		if n := utf8.RuneCountInString(trimmed); n > MaxReasonChars {
			t.Errorf("a trimmed reason is %d characters, over the %d limit", n, MaxReasonChars)
		}
		if !fits && !strings.HasSuffix(trimmed, "…") {
			t.Errorf("a trimmed reason does not show it was cut: %q", trimmed)
		}
	}
}
