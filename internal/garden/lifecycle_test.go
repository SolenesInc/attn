package garden

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/protocol"
)

const (
	me    = "sess-me"
	other = "sess-you"
)

func alive(protocol.SessionID) bool { return true }

func gone(protocol.SessionID) bool { return false }

func seedIn(status string, tender Tender) Seed {
	return Seed{
		ID: "s-7k3f9m", Title: "a seed", Status: status,
		TenderSession: tender.Session, TenderMember: tender.Member,
	}
}

func TestTransitionMatrix(t *testing.T) {
	held := Tender{Session: other, Member: "alder"}
	mine := Tender{Session: me, Member: "trellis"}

	cases := []struct {
		name   string
		seed   Seed
		verb   Verb
		want   string
		refuse string
		force  bool
	}{
		{name: "planted/tend", seed: seedIn(StatusPlanted, Tender{}), verb: VerbTend, want: StatusGrowing},
		{name: "planted/park", seed: seedIn(StatusPlanted, Tender{}), verb: VerbPark, want: StatusDormant},
		{name: "planted/harvest", seed: seedIn(StatusPlanted, Tender{}), verb: VerbHarvest, want: StatusHarvested},
		{name: "planted/wither", seed: seedIn(StatusPlanted, Tender{}), verb: VerbWither, want: StatusWithered},
		{name: "planted/replant", seed: seedIn(StatusPlanted, Tender{}), verb: VerbReplant, refuse: "already planted"},

		{name: "growing by me/tend", seed: seedIn(StatusGrowing, mine), verb: VerbTend, want: StatusGrowing},
		{name: "growing by me/park", seed: seedIn(StatusGrowing, mine), verb: VerbPark, want: StatusDormant},
		{name: "growing by me/harvest", seed: seedIn(StatusGrowing, mine), verb: VerbHarvest, want: StatusHarvested},
		{name: "growing by me/wither", seed: seedIn(StatusGrowing, mine), verb: VerbWither, want: StatusWithered},
		{name: "growing by me/replant", seed: seedIn(StatusGrowing, mine), verb: VerbReplant, want: StatusPlanted},

		{name: "growing by another/tend", seed: seedIn(StatusGrowing, held), verb: VerbTend, refuse: "takes it from them"},
		{name: "growing by another/park", seed: seedIn(StatusGrowing, held), verb: VerbPark, refuse: "takes it from them"},
		{name: "growing by another/harvest", seed: seedIn(StatusGrowing, held), verb: VerbHarvest, refuse: "takes it from them"},
		{name: "growing by another/wither", seed: seedIn(StatusGrowing, held), verb: VerbWither, refuse: "takes it from them"},
		{name: "growing by another/replant", seed: seedIn(StatusGrowing, held), verb: VerbReplant, refuse: "takes it from them"},

		{name: "growing by another/tend names them", seed: seedIn(StatusGrowing, held), verb: VerbTend, refuse: "tended by Alder"},

		{name: "forced/tend", seed: seedIn(StatusGrowing, held), verb: VerbTend, force: true, want: StatusGrowing},
		{name: "forced/park", seed: seedIn(StatusGrowing, held), verb: VerbPark, force: true, want: StatusDormant},
		{name: "forced/harvest", seed: seedIn(StatusGrowing, held), verb: VerbHarvest, force: true, want: StatusHarvested},
		{name: "forced/wither", seed: seedIn(StatusGrowing, held), verb: VerbWither, force: true, want: StatusWithered},
		{name: "forced/replant", seed: seedIn(StatusGrowing, held), verb: VerbReplant, force: true, want: StatusPlanted},

		{name: "force with nobody to take from", seed: seedIn(StatusPlanted, Tender{}), verb: VerbPark, force: true, want: StatusDormant},

		{name: "dormant/tend", seed: seedIn(StatusDormant, Tender{}), verb: VerbTend, want: StatusGrowing},
		{name: "dormant/park", seed: seedIn(StatusDormant, Tender{}), verb: VerbPark, refuse: "already dormant"},
		{name: "dormant/harvest", seed: seedIn(StatusDormant, Tender{}), verb: VerbHarvest, want: StatusHarvested},
		{name: "dormant/wither", seed: seedIn(StatusDormant, Tender{}), verb: VerbWither, want: StatusWithered},
		{name: "dormant/replant", seed: seedIn(StatusDormant, Tender{}), verb: VerbReplant, want: StatusPlanted},

		{name: "harvested/tend", seed: seedIn(StatusHarvested, Tender{}), verb: VerbTend, refuse: "reopens before it moves"},
		{name: "harvested/park", seed: seedIn(StatusHarvested, Tender{}), verb: VerbPark, refuse: "reopens before it moves"},
		{name: "harvested/harvest", seed: seedIn(StatusHarvested, Tender{}), verb: VerbHarvest, refuse: "already harvested"},
		{name: "harvested/wither", seed: seedIn(StatusHarvested, Tender{}), verb: VerbWither, refuse: "reopens before it moves"},
		{name: "harvested/replant", seed: seedIn(StatusHarvested, Tender{}), verb: VerbReplant, want: StatusPlanted},

		{name: "withered/tend", seed: seedIn(StatusWithered, Tender{}), verb: VerbTend, refuse: "reopens before it moves"},
		{name: "withered/park", seed: seedIn(StatusWithered, Tender{}), verb: VerbPark, refuse: "reopens before it moves"},
		{name: "withered/harvest", seed: seedIn(StatusWithered, Tender{}), verb: VerbHarvest, refuse: "reopens before it moves"},
		{name: "withered/wither", seed: seedIn(StatusWithered, Tender{}), verb: VerbWither, refuse: "already withered"},
		{name: "withered/replant", seed: seedIn(StatusWithered, Tender{}), verb: VerbReplant, want: StatusPlanted},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.seed
			reason := ""
			if tc.verb == VerbHarvest || tc.verb == VerbWither {
				reason = "because"
			}
			next, err := Transition(tc.seed, tc.verb, Ask{Actor: mine, Reason: reason, Force: tc.force}, alive)
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
			if tc.seed.Status == next.Status && tc.seed.TenderSession != seedIn(tc.seed.Status, Tender{Session: tc.seed.TenderSession, Member: tc.seed.TenderMember}).TenderSession {
				t.Fatal("the input seed was mutated")
			}
		})
	}
}

func TestTransitionClaimsAndReasons(t *testing.T) {
	mine := Tender{Session: me, Member: "trellis"}
	held := Tender{Session: other, Member: "alder"}
	armed := func(seed Seed) Seed {
		seed.HarvestWhen = &HarvestCondition{PullRequest: "github.com:victorarias/attn#113", URL: "https://github.com/victorarias/attn/pull/113", SetAt: "2026-09-02T00:21:00Z"}
		return seed
	}
	withReason := func(seed Seed, reason string) Seed {
		seed.Reason = reason
		return seed
	}
	memberOnly := func(member string) Seed {
		return Seed{ID: "s-7k3f9m", Status: StatusGrowing, TenderMember: member}
	}
	cases := []struct {
		name          string
		seed          Seed
		verb          Verb
		ask           Ask
		sessionGone   bool
		refuse        []string
		tender        Tender
		reason        string
		keepCondition bool
	}{
		{name: "tend records the tender", seed: seedIn(StatusPlanted, Tender{}), verb: VerbTend, ask: Ask{Actor: mine}, tender: mine},
		{name: "park releases the claim", seed: seedIn(StatusGrowing, mine), verb: VerbPark, ask: Ask{Actor: mine}},
		{name: "harvest releases the claim and keeps its reason", seed: seedIn(StatusGrowing, mine), verb: VerbHarvest, ask: Ask{Actor: mine, Reason: "  done  "}, reason: "done"},
		{name: "wither releases the claim", seed: seedIn(StatusGrowing, mine), verb: VerbWither, ask: Ask{Actor: mine, Reason: "done"}, reason: "done"},
		{name: "a wordless wither is fine", seed: seedIn(StatusPlanted, Tender{}), verb: VerbWither, ask: Ask{Actor: mine}},
		{name: "a wordless harvest is refused", seed: seedIn(StatusPlanted, Tender{}), verb: VerbHarvest, ask: Ask{Actor: mine, Reason: "  "}, refuse: []string{"-m"}},
		{name: "tend drops no reason silently", seed: seedIn(StatusPlanted, Tender{}), verb: VerbTend, ask: Ask{Actor: mine, Reason: "some words"}, refuse: []string{"tend", "attn seed note", "s-7k3f9m"}},
		{name: "park drops no reason silently", seed: seedIn(StatusPlanted, Tender{}), verb: VerbPark, ask: Ask{Actor: mine, Reason: "some words"}, refuse: []string{"park", "attn seed note", "s-7k3f9m"}},
		{name: "replant drops no reason silently", seed: seedIn(StatusHarvested, Tender{}), verb: VerbReplant, ask: Ask{Actor: mine, Reason: "some words"}, refuse: []string{"replant", "attn seed note", "s-7k3f9m"}},
		{name: "replant clears the closing reason", seed: withReason(seedIn(StatusHarvested, Tender{}), "shipped it"), verb: VerbReplant, ask: Ask{Actor: mine}},
		{name: "a reason past the limit names both numbers and the log", seed: seedIn(StatusPlanted, Tender{}), verb: VerbHarvest, ask: Ask{Actor: mine, Reason: strings.Repeat("x", MaxReasonChars+1)}, refuse: []string{"401", "400", "attn seed note"}},
		{name: "the reason limit counts characters, not bytes", seed: seedIn(StatusPlanted, Tender{}), verb: VerbHarvest, ask: Ask{Actor: mine, Reason: strings.Repeat("🌱", MaxReasonChars)}, reason: strings.Repeat("🌱", MaxReasonChars)},
		{name: "one character past the limit is refused", seed: seedIn(StatusPlanted, Tender{}), verb: VerbHarvest, ask: Ask{Actor: mine, Reason: strings.Repeat("🌱", MaxReasonChars+1)}, refuse: []string{"401 characters"}},
		{name: "a tend that names nobody asks for a member", seed: seedIn(StatusPlanted, Tender{}), verb: VerbTend, ask: Ask{}, refuse: []string{"--member"}},
		{name: "a live claim names its member and the way forward", seed: seedIn(StatusGrowing, held), verb: VerbTend, ask: Ask{Actor: mine}, refuse: []string{"s-7k3f9m", "Alder", "attn seed note"}},
		{name: "a live claim without a member names its session", seed: seedIn(StatusGrowing, Tender{Session: other}), verb: VerbTend, ask: Ask{Actor: Tender{Session: me}}, refuse: []string{other}},
		{name: "a member-only claim refuses another member", seed: memberOnly("trellis"), verb: VerbTend, ask: Ask{Actor: Tender{Member: "alder"}}, refuse: []string{"Trellis"}},
		{name: "a member-only claim lets its member back in", seed: memberOnly("trellis"), verb: VerbTend, ask: Ask{Actor: Tender{Member: "trellis"}}, tender: Tender{Member: "trellis"}},
		{name: "a member-only claim is not taken by a session using that name", seed: memberOnly("trellis"), verb: VerbTend, ask: Ask{Actor: Tender{Session: "sess-a", Member: "trellis"}}, refuse: []string{"Trellis"}},
		{name: "the holding session is known by its id, not its label", seed: seedIn(StatusGrowing, Tender{Session: "sess-a", Member: "trellis"}), verb: VerbTend, ask: Ask{Actor: Tender{Session: "sess-a", Member: "keel"}}, tender: Tender{Session: "sess-a", Member: "keel"}},
		{name: "a claim whose session ended passes to the next tender", seed: seedIn(StatusGrowing, held), verb: VerbTend, ask: Ask{Actor: mine}, sessionGone: true, tender: mine},
		{name: "a claim whose session ended can be parked", seed: seedIn(StatusGrowing, held), verb: VerbPark, ask: Ask{Actor: mine}, sessionGone: true},
		{name: "a claim whose session ended can be harvested", seed: seedIn(StatusGrowing, held), verb: VerbHarvest, ask: Ask{Actor: mine, Reason: "done"}, sessionGone: true, reason: "done"},
		{name: "a claim whose session ended can be withered", seed: seedIn(StatusGrowing, held), verb: VerbWither, ask: Ask{Actor: mine, Reason: "done"}, sessionGone: true, reason: "done"},
		{name: "a claim whose session ended can be replanted", seed: seedIn(StatusGrowing, held), verb: VerbReplant, ask: Ask{Actor: mine}, sessionGone: true},
		{name: "a member-only claim does not end with sessions", seed: memberOnly("trellis"), verb: VerbTend, ask: Ask{Actor: Tender{Member: "alder"}}, sessionGone: true, refuse: []string{"Trellis"}},
		{name: "harvest drops the harvest condition", seed: armed(seedIn(StatusPlanted, Tender{})), verb: VerbHarvest, ask: Ask{Actor: mine, Reason: "the pull request landed"}, reason: "the pull request landed"},
		{name: "wither drops the harvest condition", seed: armed(seedIn(StatusPlanted, Tender{})), verb: VerbWither, ask: Ask{Actor: mine}},
		{name: "park keeps the harvest condition", seed: armed(seedIn(StatusPlanted, Tender{})), verb: VerbPark, ask: Ask{Actor: mine}, keepCondition: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live := alive
			if tc.sessionGone {
				live = gone
			}
			next, err := Transition(tc.seed, tc.verb, tc.ask, live)
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
			if next.Tender() != tc.tender {
				t.Errorf("tender = %+v, want %+v", next.Tender(), tc.tender)
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
