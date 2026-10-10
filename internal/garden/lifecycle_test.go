package garden

import (
	"errors"
	"fmt"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTransitionMatrix(t *testing.T) {
	key, _ := who.ParseMemberKey("keel")
	member := who.Member(key)
	session := who.PartyOfEndedSession("plain")
	b := who.NewBindings(func(id protocol.SessionID) (string, bool) { return "profile", id == "plain" }, nil, nil)
	statuses := []string{StatusPlanted, StatusGrowing, StatusDormant, StatusHarvested, StatusWithered}
	for _, status := range statuses {
		for _, tender := range []who.Party{{}, session, member, who.PartyOfEndedSession("ended")} {
			for _, by := range []who.Actor{session.Actor(), member.Actor(), who.User(), who.Attn()} {
				for _, force := range []bool{false, true} {
					for _, verb := range Verbs {
						name := fmt.Sprintf("%s/%s/%s/%s/force=%v", status, tender, by, verb, force)
						t.Run(name, func(t *testing.T) {
							seed := Seed{ID: "s-7k3f9m", Status: status, Claim: Claim{tender: tender}, Planter: who.User()}
							before := seed
							ask := Ask{By: by, Force: force}
							if verb == VerbHarvest || verb == VerbWither {
								ask.Reason = "done"
							}
							var next Seed
							var err error
							if verb == VerbTend {
								next, err = Tend(seed, session, ask, b)
							} else {
								next, err = Transition(seed, verb, ask, b)
							}
							if !reflect.DeepEqual(seed, before) {
								t.Fatal("changed input")
							}
							permitted := false
							for _, from := range moves[verb].from {
								permitted = permitted || status == from
							}
							_, claimed := seed.Claim.Lasts(b)
							takeover := claimed && tender.Actor() != by && !force && (verb != VerbTend || tender != session)
							if !permitted || takeover {
								if err == nil {
									t.Fatal("allowed a refused move")
								}
								if permitted && takeover {
									var refused *TakeoverRefused
									if !errors.As(err, &refused) || refused.Tender != tender {
										t.Fatalf("takeover refusal: %v", err)
									}
								}
								return
							}
							if err != nil {
								t.Fatal(err)
							}
							if next.Status != moves[verb].to {
								t.Fatalf("status %s", next.Status)
							}
							got, stored := next.Claim.Tender()
							if verb == VerbTend {
								if !stored || got != session {
									t.Fatalf("claim %v", got)
								}
							} else if stored {
								t.Fatalf("transition retained claim %v", got)
							}
						})
					}
				}
			}
		}
	}
}

func TestClaimEncodingAndReasonRules(t *testing.T) {
	b := who.NewBindings(func(protocol.SessionID) (string, bool) { return "profile", true }, nil, nil)
	party := who.PartyOfEndedSession("plain")
	for _, row := range []struct {
		name, reason string
		verb         Verb
		wantErr      bool
	}{
		{"harvest needs reason", "", VerbHarvest, true}, {"park accepts no reason", "done", VerbPark, true},
		{"harvest records reason", "done", VerbHarvest, false}, {"wither may be wordless", "", VerbWither, false},
		{"oversized reason", strings.Repeat("x", MaxReasonChars+1), VerbWither, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			seed := Seed{ID: "s-7k3f9m", Status: StatusPlanted}
			_, err := Transition(seed, row.verb, Ask{By: party.Actor(), Reason: row.reason}, b)
			if (err != nil) != row.wantErr {
				t.Fatalf("%v", err)
			}
		})
	}
	for _, party := range []who.Party{party, who.Member(func() who.MemberKey { k, _ := who.ParseMemberKey("keel"); return k }())} {
		seed, err := Assign(Seed{ID: "s-7k3f9m", Status: StatusPlanted, Planter: who.User()}, party)
		if err != nil {
			t.Fatal(err)
		}
		body, err := seed.Encode()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := Decode(body)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := decoded.Claim.Tender()
		if got != party {
			t.Fatalf("claim roundtrip: %s", body)
		}
	}
	if _, err := Transition(Seed{Status: StatusPlanted}, VerbTend, Ask{}, b); err == nil {
		t.Fatal("Transition accepted tend")
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
