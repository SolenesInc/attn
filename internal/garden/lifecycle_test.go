package garden

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const (
	me    = "sess-me"
	other = "sess-you"
)

func alive(string) bool { return true }

func gone(string) bool { return false }

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

func TestTransitionMovesTheTender(t *testing.T) {
	actor := Tender{Session: me, Member: "trellis"}

	claimed, err := Transition(seedIn(StatusPlanted, Tender{}), VerbTend, Ask{Actor: actor}, alive)
	if err != nil {
		t.Fatalf("tend: %v", err)
	}
	if claimed.TenderSession != me || claimed.TenderMember != "trellis" {
		t.Fatalf("tend did not record the tender: %+v", claimed)
	}

	for _, tc := range []struct {
		verb   Verb
		reason string
	}{{VerbPark, ""}, {VerbHarvest, "done"}, {VerbWither, "done"}} {
		released, err := Transition(claimed, tc.verb, Ask{Actor: actor, Reason: tc.reason}, alive)
		if err != nil {
			t.Fatalf("%s: %v", tc.verb, err)
		}
		if released.TenderSession != "" || released.TenderMember != "" {
			t.Fatalf("%s left the seed claimed: %+v", tc.verb, released)
		}
	}
}

func TestTransitionRefusesAReasonTheMoveWouldDrop(t *testing.T) {
	actor := Tender{Session: me}
	for _, verb := range []Verb{VerbTend, VerbPark, VerbReplant} {
		from := StatusPlanted
		if verb == VerbReplant {
			from = StatusHarvested
		}
		_, err := Transition(seedIn(from, Tender{}), verb, Ask{Actor: actor, Reason: "some words"}, alive)
		if err == nil {
			t.Fatalf("%s swallowed a reason instead of refusing it", verb)
		}
		for _, want := range []string{string(verb), "attn seed note", "s-7k3f9m"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%s refusal = %q, want it to say %q", verb, err, want)
			}
		}
	}
}

func TestReplantClearsTheClosingReason(t *testing.T) {
	actor := Tender{Session: me}
	harvested, err := Transition(seedIn(StatusPlanted, Tender{}), VerbHarvest, Ask{Actor: actor, Reason: "shipped it"}, alive)
	if err != nil {
		t.Fatalf("harvest: %v", err)
	}
	if harvested.Reason != "shipped it" {
		t.Fatalf("harvest did not record the reason: %+v", harvested)
	}
	replanted, err := Transition(harvested, VerbReplant, Ask{Actor: actor}, alive)
	if err != nil {
		t.Fatalf("replant: %v", err)
	}
	if replanted.Reason != "" {
		t.Fatalf("replant kept the closing reason %q", replanted.Reason)
	}
}

func TestTendRefusalNamesTheTenderAndTheWayForward(t *testing.T) {
	held := seedIn(StatusGrowing, Tender{Session: other, Member: "alder"})
	_, err := Transition(held, VerbTend, Ask{Actor: Tender{Session: me, Member: "trellis"}}, alive)
	if err == nil {
		t.Fatal("a second session was allowed to take a live claim")
	}
	for _, want := range []string{held.ID, "Alder", "attn seed note"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal does not name %q:\n%s", want, err)
		}
	}
}

func TestTenderDisplayName_WritesAMemberAsANameAndLeavesASessionAlone(t *testing.T) {
	member := Tender{Session: "sess-a", Member: "trellis"}
	if got := member.DisplayName(); got != "Trellis" {
		t.Errorf("DisplayName() = %q, want Trellis", got)
	}
	if got := member.Name(); got != "trellis" {
		t.Errorf("Name() = %q, want the stored id", got)
	}
	session := Tender{Session: "sess-a"}
	if got := session.DisplayName(); got != "sess-a" {
		t.Errorf("DisplayName() = %q, want the session id untouched", got)
	}
	if got := (Tender{}).DisplayName(); got != "" {
		t.Errorf("an unnamed tender displays as %q, want empty", got)
	}
}

// internal/pty/manager.go strips ATTN_SESSION_ID from a shell pane, so a person
// in a pane claims with --member and carries no session at all.
func TestTendRefusesAnotherMemberWhenNeitherCarriesASession(t *testing.T) {
	held := Seed{ID: "s-abc123", Status: StatusGrowing, TenderMember: "trellis"}

	if _, err := Transition(held, VerbTend, Ask{Actor: Tender{Member: "alder"}}, alive); err == nil {
		t.Fatal("a different member took a live claim; the seed has one tender at a time")
	} else if !strings.Contains(err.Error(), "Trellis") {
		t.Fatalf("refusal does not name who holds it: %v", err)
	}

	if _, err := Transition(held, VerbTend, Ask{Actor: Tender{Member: "trellis"}}, alive); err != nil {
		t.Fatalf("trellis was refused their own claim: %v", err)
	}
}

func TestTendIdentifiesASessionByItsIDNotItsLabel(t *testing.T) {
	held := Seed{ID: "s-abc123", Status: StatusGrowing, TenderSession: "sess-a", TenderMember: "trellis"}

	if _, err := Transition(held, VerbTend, Ask{Actor: Tender{Session: "sess-a", Member: "keel"}}, alive); err != nil {
		t.Fatalf("the holding session was refused its own claim: %v", err)
	}
	memberOnly := Seed{ID: "s-abc123", Status: StatusGrowing, TenderMember: "trellis"}
	if _, err := Transition(memberOnly, VerbTend, Ask{Actor: Tender{Session: "sess-a", Member: "trellis"}}, alive); err == nil {
		t.Fatal("a session took a claim held with no session id")
	}
}

func TestTendReleasesASeedWhoseTenderSessionIsGone(t *testing.T) {
	held := seedIn(StatusGrowing, Tender{Session: other, Member: "alder"})

	if got := held.Tender().Holds(gone); got {
		t.Fatal("a tender whose session the daemon no longer knows still holds its seed")
	}
	claimed, err := Transition(held, VerbTend, Ask{Actor: Tender{Session: me, Member: "trellis"}}, gone)
	if err != nil {
		t.Fatalf("a successor was refused a seed whose tender's session ended: %v", err)
	}
	if claimed.TenderSession != me || claimed.TenderMember != "trellis" {
		t.Fatalf("the claim did not move to the successor: %+v", claimed)
	}

	pane := Seed{ID: "s-abc123", Status: StatusGrowing, TenderMember: "trellis"}
	if !pane.Tender().Holds(gone) {
		t.Fatal("a member-only tender was released by a session rule that cannot see them")
	}
	if _, err := Transition(pane, VerbTend, Ask{Actor: Tender{Member: "alder"}}, gone); err == nil {
		t.Fatal("a member-only claim was taken because no session was alive")
	}
}

func TestEveryMoveAllowsASeedWhoseTenderSessionEnded(t *testing.T) {
	actor := Tender{Session: me, Member: "trellis"}
	for _, verb := range Verbs {
		t.Run(string(verb), func(t *testing.T) {
			reason := ""
			if verb == VerbHarvest || verb == VerbWither {
				reason = "done"
			}
			if _, err := Transition(seedIn(StatusGrowing, Tender{Session: other, Member: "alder"}), verb,
				Ask{Actor: actor, Reason: reason}, gone); err != nil {
				t.Fatalf("%s refused after the holder session ended: %v", verb, err)
			}
		})
	}
}

func TestTendRefusalFallsBackToTheSessionID(t *testing.T) {
	held := seedIn(StatusGrowing, Tender{Session: other})
	_, err := Transition(held, VerbTend, Ask{Actor: Tender{Session: me}}, alive)
	if err == nil || !strings.Contains(err.Error(), other) {
		t.Fatalf("a member-less tender did not hold the claim by name: %v", err)
	}
}

func TestTendNeedsSomebodyToRecord(t *testing.T) {
	_, err := Transition(seedIn(StatusPlanted, Tender{}), VerbTend, Ask{Actor: Tender{}}, alive)
	if err == nil || !strings.Contains(err.Error(), "--member") {
		t.Fatalf("a tend that names nobody was accepted or refused unhelpfully: %v", err)
	}
}

func TestHarvestNeedsAReason(t *testing.T) {
	_, err := Transition(seedIn(StatusPlanted, Tender{}), VerbHarvest, Ask{Actor: Tender{Session: me}, Reason: "  "}, alive)
	if err == nil || !strings.Contains(err.Error(), "-m") {
		t.Fatalf("a wordless harvest was accepted or refused unhelpfully: %v", err)
	}
	withered, err := Transition(seedIn(StatusPlanted, Tender{}), VerbWither, Ask{Actor: Tender{Session: me}}, alive)
	if err != nil {
		t.Fatalf("a wordless wither was refused: %v", err)
	}
	if withered.Status != StatusWithered {
		t.Fatalf("wither landed in %q", withered.Status)
	}
}

func TestReasonLimitNamesItselfAndPointsAtTheLog(t *testing.T) {
	_, err := Transition(seedIn(StatusPlanted, Tender{}), VerbHarvest, Ask{Actor: Tender{Session: me}, Reason: strings.Repeat("x", MaxReasonChars+1)}, alive)
	if err == nil {
		t.Fatal("an oversized reason was accepted")
	}
	for _, want := range []string{"401", "400", "attn seed note"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the limit refusal does not name %q: %s", want, err)
		}
	}
}

func TestReasonLimitCountsUnicodeCharacters(t *testing.T) {
	seed := seedIn(StatusPlanted, Tender{})
	if _, err := Transition(seed, VerbHarvest, Ask{
		Actor: Tender{Session: me}, Reason: strings.Repeat("🌱", MaxReasonChars),
	}, alive); err != nil {
		t.Fatalf("%d Unicode characters were refused: %v", MaxReasonChars, err)
	}

	_, err := Transition(seed, VerbHarvest, Ask{
		Actor: Tender{Session: me}, Reason: strings.Repeat("🌱", MaxReasonChars+1),
	}, alive)
	if err == nil || !strings.Contains(err.Error(), "401 characters") {
		t.Fatalf("Unicode over-limit error = %v", err)
	}
}

func TestParseVerbNamesTheWholeSet(t *testing.T) {
	for _, verb := range Verbs {
		if got, err := ParseVerb(string(verb)); err != nil || got != verb {
			t.Fatalf("ParseVerb(%q) = %q, %v", verb, got, err)
		}
	}
	if _, err := ParseVerb("  HARVEST "); err != nil {
		t.Fatalf("a verb is read case- and space-insensitively: %v", err)
	}
	_, err := ParseVerb("compost")
	if err == nil {
		t.Fatal("an unknown verb was accepted")
	}
	for _, verb := range Verbs {
		if !strings.Contains(err.Error(), string(verb)) {
			t.Fatalf("the refusal does not offer %q: %s", verb, err)
		}
	}
}

func TestValidateNote(t *testing.T) {
	if err := ValidateNote("  \n "); err == nil {
		t.Fatal("an empty note was accepted")
	}
	if err := ValidateNote(strings.Repeat("x", MaxNoteBytes+1)); err == nil {
		t.Fatal("a note past the limit was accepted")
	} else if !strings.Contains(err.Error(), strconv.Itoa(MaxNoteBytes+1)) {
		t.Fatalf("the limit refusal does not name the ask: %v", err)
	}
	if MaxNoteBytes >= 64<<10 {
		t.Fatalf("MaxNoteBytes is %d, at or past the 64KiB socket frame it travels through", MaxNoteBytes)
	}
	if err := ValidateNote("what happened"); err != nil {
		t.Fatalf("a real note was refused: %v", err)
	}
}

func TestParseNoteKindNamesTheWholeSet(t *testing.T) {
	if got, err := ParseNoteKind(""); err != nil || got != NoteKindNote {
		t.Fatalf("ParseNoteKind(\"\") = %q, %v; want the plain note", got, err)
	}
	for _, kind := range NoteKinds {
		if got, err := ParseNoteKind(kind); err != nil || got != kind {
			t.Fatalf("ParseNoteKind(%q) = %q, %v", kind, got, err)
		}
	}
	if got, err := ParseNoteKind(" HANDOFF "); err != nil || got != NoteKindHandoff {
		t.Fatalf("a kind is read case- and space-insensitively: %q, %v", got, err)
	}
	_, err := ParseNoteKind("farewell")
	if err == nil {
		t.Fatal("an unknown note kind was accepted")
	}
	for _, kind := range NoteKinds {
		if !strings.Contains(err.Error(), kind) {
			t.Fatalf("the refusal does not offer %q: %s", kind, err)
		}
	}
}

func TestNoteIDsAreTheirOwnShape(t *testing.T) {
	id, err := NewNoteID()
	if err != nil {
		t.Fatalf("NewNoteID: %v", err)
	}
	if !strings.HasPrefix(id, "n-") || len(id) != len("n-")+idBodyLen {
		t.Fatalf("note id %q is not n- plus %d characters", id, idBodyLen)
	}
	if err := ValidateID(id); err == nil {
		t.Fatalf("note id %q validates as a seed id", id)
	}
}
