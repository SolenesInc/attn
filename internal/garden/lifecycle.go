package garden

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/who"
)

type Verb string

const (
	VerbTend    Verb = "tend"
	VerbPark    Verb = "park"
	VerbHarvest Verb = "harvest"
	VerbWither  Verb = "wither"
	VerbReplant Verb = "replant"
)

var Verbs = []Verb{VerbTend, VerbPark, VerbHarvest, VerbWither, VerbReplant}

type Claim struct{ tender who.Party }

func (c Claim) Tender() (who.Party, bool)              { return c.tender, !c.tender.IsZero() }
func (c Claim) Lasts(b who.Bindings) (who.Party, bool) { return c.tender, b.Lasts(c.tender) }
func (c Claim) IsZero() bool                           { return c.tender.IsZero() }
func (c Claim) MarshalText() ([]byte, error)           { return c.tender.MarshalText() }
func (c *Claim) UnmarshalText(text []byte) error       { return c.tender.UnmarshalText(text) }

type Ask struct {
	By                   who.Actor
	Reason               string
	Force                bool
	DirectlyNotified     who.Party
	SuppressNotification bool
}

type TakeoverRefused struct {
	SeedID string
	Verb   Verb
	Tender who.Party
}

func (e *TakeoverRefused) Error() string {
	return fmt.Sprintf("%s is being tended by %s; pass --force to %s it", e.SeedID, e.Tender, e.Verb)
}

func Tend(seed Seed, claimant who.Party, ask Ask, b who.Bindings) (Seed, error) {
	if claimant.IsZero() {
		return Seed{}, fmt.Errorf("tending %s needs a party that claims it", seed.ID)
	}
	next, err := transition(seed, VerbTend, ask, b, claimant)
	if err != nil {
		return Seed{}, err
	}
	next.Claim = Claim{tender: claimant}
	return next, nil
}
func Assign(seed Seed, to who.Party) (Seed, error) {
	if to.IsZero() {
		return Seed{}, fmt.Errorf("assigning %s needs a party that claims it", seed.ID)
	}
	if !slices.Contains(moves[VerbTend].from, seed.Status) {
		return Seed{}, refuseState(seed, VerbTend, moves[VerbTend])
	}
	seed.Status = StatusGrowing
	seed.Claim = Claim{tender: to}
	return seed, nil
}

type move struct {
	to          string
	from        []string
	claims      bool
	needsReason bool
	keepsReason bool
	resume      string
}

var moves = map[Verb]move{
	VerbTend: {
		to:     StatusGrowing,
		from:   []string{StatusPlanted, StatusDormant, StatusGrowing},
		claims: true,
		resume: "attn seed park",
	},
	VerbPark: {
		to:     StatusDormant,
		from:   []string{StatusPlanted, StatusGrowing},
		resume: "attn seed tend",
	},
	VerbHarvest: {
		to:          StatusHarvested,
		from:        []string{StatusPlanted, StatusGrowing, StatusDormant},
		needsReason: true,
		keepsReason: true,
		resume:      "attn seed replant",
	},
	VerbWither: {
		to:          StatusWithered,
		from:        []string{StatusPlanted, StatusGrowing, StatusDormant},
		keepsReason: true,
		resume:      "attn seed replant",
	},
	VerbReplant: {
		from:   []string{StatusHarvested, StatusWithered, StatusDormant, StatusGrowing},
		to:     StatusPlanted,
		resume: "attn seed tend",
	},
}

func Closed(status string) bool {
	return status == StatusHarvested || status == StatusWithered
}

func ParseVerb(raw string) (Verb, error) {
	verb := Verb(strings.TrimSpace(strings.ToLower(raw)))
	if _, ok := moves[verb]; ok {
		return verb, nil
	}
	names := make([]string, 0, len(Verbs))
	for _, v := range Verbs {
		names = append(names, string(v))
	}
	return "", fmt.Errorf("%q is not something a seed does; the moves are %s", raw, strings.Join(names, ", "))
}

func Transition(seed Seed, verb Verb, ask Ask, b who.Bindings) (Seed, error) {
	if verb == VerbTend {
		return Seed{}, fmt.Errorf("tend needs a claimant; use Tend")
	}
	return transition(seed, verb, ask, b, who.Party{})
}

func transition(seed Seed, verb Verb, ask Ask, b who.Bindings, claimant who.Party) (Seed, error) {
	rule, ok := moves[verb]
	if !ok {
		return Seed{}, fmt.Errorf("%q is not something a seed does", verb)
	}
	reason := strings.TrimSpace(ask.Reason)

	if !slices.Contains(rule.from, seed.Status) {
		return Seed{}, refuseState(seed, verb, rule)
	}
	if tender, claimed := seed.Claim.Lasts(b); claimed && tender != claimant && tender.Actor() != ask.By && !ask.Force {
		return Seed{}, &TakeoverRefused{SeedID: seed.ID, Verb: verb, Tender: tender}
	}
	if rule.needsReason && reason == "" {
		return Seed{}, fmt.Errorf(
			"harvesting %s records what got done: attn seed harvest %s -m \"what got done\"", seed.ID, seed.ID)
	}
	if reason != "" && !rule.keepsReason {
		return Seed{}, fmt.Errorf(
			"%s records no reason — harvest and wither are the moves that close a seed with one. Put it on the log instead: attn seed note %s -m \"…\"",
			verb, seed.ID)
	}
	if n := utf8.RuneCountInString(reason); n > MaxReasonChars {
		return Seed{}, fmt.Errorf(
			"that reason is %d characters and the limit is %d; the detail belongs on the log (`attn seed note %s -m …`)",
			n, MaxReasonChars, seed.ID)
	}

	next := seed
	next.Status = rule.to
	next.Claim = Claim{}

	switch {
	case rule.keepsReason:
		next.Reason = reason
	case verb == VerbReplant:
		next.Reason = ""
	}
	if Closed(next.Status) {
		next.HarvestWhen = nil
	}
	return next, nil
}

func refuseState(seed Seed, verb Verb, rule move) error {
	switch {
	case seed.Status == rule.to:
		return fmt.Errorf("%s is already %s; `%s %s` is the way out of it", seed.ID, seed.Status, rule.resume, seed.ID)
	case Closed(seed.Status):
		return fmt.Errorf(
			"%s is %s, and a closed seed reopens before it moves again: `attn seed replant %s`, then %s it",
			seed.ID, seed.Status, seed.ID, verb)
	default:
		return fmt.Errorf("%s is %s and cannot be %sed from there", seed.ID, seed.Status, verb)
	}
}

type Note struct {
	ID       string             `json:"id"`
	Seed     string             `json:"seed"`
	Kind     string             `json:"kind"`
	Body     string             `json:"body"`
	Author   who.Actor          `json:"author"`
	Artifact *ArtifactReference `json:"artifact,omitempty"`
}

const (
	NoteKindNote    = "note"
	NoteKindHandoff = "handoff"
	NoteKindAttach  = "attach"
	NoteKindDetach  = "detach"
)

var NoteKinds = []string{NoteKindNote, NoteKindHandoff, NoteKindAttach, NoteKindDetach}

func CarriesArtifact(kind string) bool {
	return kind == NoteKindAttach || kind == NoteKindDetach
}

func ParseNoteKind(raw string) (string, error) {
	kind := strings.TrimSpace(strings.ToLower(raw))
	if kind == "" {
		return NoteKindNote, nil
	}
	if slices.Contains(NoteKinds, kind) {
		return kind, nil
	}
	return "", fmt.Errorf("%q is not a kind of note; the kinds are %s", raw, strings.Join(NoteKinds, ", "))
}

const (
	MaxNoteBytes   = 32 << 10
	MaxReasonChars = 400
	ShowNotes      = 5
)

func TrimReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) <= MaxReasonChars {
		return reason
	}
	return strings.TrimSpace(string([]rune(reason)[:MaxReasonChars-1])) + "…"
}

func ValidateNote(body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("a note needs something in it: `attn seed note <id> -m \"what happened\"`")
	}
	if n := len(body); n > MaxNoteBytes {
		return fmt.Errorf("that note is %d bytes and the limit is %d; a note is what happened and what you learned, not an archive", n, MaxNoteBytes)
	}
	return nil
}

func NewNoteID() (string, error) { return mintID(noteIDPrefix) }

func (n Note) Encode() ([]byte, error) { return json.Marshal(n) }

func DecodeNote(body []byte) (Note, error) {
	var note Note
	if err := json.Unmarshal(body, &note); err != nil {
		return Note{}, fmt.Errorf("this note's stored body is not readable: %w", err)
	}
	return note, nil
}
