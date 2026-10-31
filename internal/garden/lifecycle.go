package garden

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/shortid"
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

type Ask struct {
	Actor                   who.Actor
	Claimant                who.Party
	Reason                  string
	Force                   bool
	CauseSession            protocol.SessionID
	DirectlyNotifiedSession protocol.SessionID
	SuppressNotification    bool
}

type move struct {
	to          string
	from        []string
	claims      bool
	needsReason bool
	keepsReason bool
}

var moves = map[Verb]move{
	VerbTend: {
		to:     StatusGrowing,
		from:   []string{StatusPlanted, StatusDormant, StatusGrowing},
		claims: true,
	},
	VerbPark: {
		to:   StatusDormant,
		from: []string{StatusPlanted, StatusGrowing},
	},
	VerbHarvest: {
		to:          StatusHarvested,
		from:        []string{StatusPlanted, StatusGrowing, StatusDormant},
		needsReason: true,
		keepsReason: true,
	},
	VerbWither: {
		to:          StatusWithered,
		from:        []string{StatusPlanted, StatusGrowing, StatusDormant},
		keepsReason: true,
	},
	VerbReplant: {
		from: []string{StatusHarvested, StatusWithered, StatusDormant, StatusGrowing},
		to:   StatusPlanted,
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

type Standing interface {
	Holds(who.Party) bool
	Oversees(actor who.Actor, held who.Party) bool
	Name(who.Party) string
}

type Means string

const (
	MeansTender   Means = "tender"
	MeansStanding Means = "standing"
	MeansForce    Means = "force"
)

type Outcome struct {
	Displaced *who.Party
	By        Means
	Seed      Seed
	Moved     bool
}

func Transition(seed Seed, verb Verb, ask Ask, standing Standing, openChildren []Seed) (Outcome, error) {
	rule, ok := moves[verb]
	if !ok {
		return Outcome{}, fmt.Errorf("%q is not something a seed does", verb)
	}
	reason := strings.TrimSpace(ask.Reason)

	if seed.Status != rule.to && !slices.Contains(rule.from, seed.Status) {
		return Outcome{}, refuseState(seed, verb)
	}
	if Closed(rule.to) && seed.Status != rule.to {
		if err := CheckPlotClose(seed, openChildren); err != nil {
			return Outcome{}, err
		}
	}
	if rule.claims && ask.Claimant.IsZero() {
		return Outcome{}, fmt.Errorf(
			"tending %s records who claims it, and the user never claims a seed; run it from an attn session, or assign it to a crew member: attn seed tend %s --for <name>", seed.ID, seed.ID)
	}
	by := MeansTender
	var displaced *who.Party
	if held := seed.Tender; seed.ClaimHolds(standing.Holds) && held != ask.Claimant && held.Actor() != ask.Actor {
		switch {
		case ask.Force:
			by = MeansForce
		case standing.Oversees(ask.Actor, held):
			by = MeansStanding
		default:
			return Outcome{}, refuseTakeover(seed, standing.Name(held))
		}
		displaced = &held
	}

	if err := ValidateReason(reason, seed.ID); err != nil {
		return Outcome{}, err
	}

	if seed.Status == rule.to && (!rule.claims || seed.Tender == ask.Claimant) {
		return Outcome{Seed: seed}, nil
	}
	if rule.needsReason && reason == "" {
		return Outcome{}, fmt.Errorf("harvesting %s records what got done: attn seed harvest %s -m \"what got done\"", seed.ID, seed.ID)
	}
	if reason != "" && !rule.keepsReason {
		return Outcome{}, fmt.Errorf("%s records no reason; put it on the log instead: attn seed note %s -m \"…\"", verb, seed.ID)
	}

	next := seed
	next.Status = rule.to
	switch {
	case rule.claims:
		next.Tender = ask.Claimant
	default:
		next.Tender = who.Party{}
	}
	switch {
	case rule.keepsReason:
		next.Reason = reason
	case verb == VerbReplant:
		next.Reason = ""
	}
	if Closed(next.Status) {
		next.HarvestWhen = nil
	}
	return Outcome{Seed: next, Moved: true, Displaced: displaced, By: by}, nil
}

func (s Seed) ClaimHolds(holds func(who.Party) bool) bool {
	return !s.Tender.IsZero() && holds(s.Tender)
}

func refuseTakeover(seed Seed, tender string) error {
	return fmt.Errorf("%s is being tended by %s; only its tender, sessions above it in its delegation chain, or the profile's chief move it; --force acts anyway and the log records it", seed.ID, tender)
}

func refuseState(seed Seed, verb Verb) error {
	switch {
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
	Author   who.Actor          `json:"author,omitzero"`
	Artifact *ArtifactReference `json:"artifact,omitempty"`
}

const (
	NoteKindNote    = "note"
	NoteKindHandoff = "handoff"
	NoteKindAttach  = "attach"
	NoteKindDetach  = "detach"
	NoteKindCheck   = "check"
	NoteKindEdit    = "edit"
	NoteKindMove    = "move"
	NoteKindLink    = "link"
	NoteKindAnswer  = "answer"
)

var NoteKinds = []string{NoteKindNote, NoteKindHandoff, NoteKindAttach, NoteKindDetach}

var EntryKinds = []string{NoteKindNote, NoteKindHandoff, NoteKindAttach, NoteKindDetach, NoteKindCheck, NoteKindEdit, NoteKindMove, NoteKindLink, NoteKindAnswer}

func ParseEntryKind(raw string) (string, error) {
	kind := strings.TrimSpace(strings.ToLower(raw))
	if slices.Contains(EntryKinds, kind) {
		return kind, nil
	}
	return "", fmt.Errorf("%q is not a kind of log entry; the kinds are %s", raw, strings.Join(EntryKinds, ", "))
}

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
	if kind == NoteKindCheck {
		return "", fmt.Errorf("a check note is written by the check move it records: `attn seed check report|confirm|reject|uncheck|reopen <id> <C#>`; a plain note keeps kind note")
	}
	if kind == NoteKindEdit {
		return "", fmt.Errorf("an edit entry is written by `attn seed edit` or `attn seed rollback`; a plain note keeps kind note")
	}
	if kind == NoteKindMove || kind == NoteKindLink {
		return "", fmt.Errorf("a %s entry is written by the change it records; a plain note keeps kind note", kind)
	}
	if kind == NoteKindAnswer {
		return "", fmt.Errorf("an answer entry is written when the user answers an ask; a plain note keeps kind note")
	}
	return "", fmt.Errorf("%q is not a kind of note; the kinds are %s", raw, strings.Join(NoteKinds, ", "))
}

const (
	MaxNoteBytes   = 32 << 10
	MaxReasonChars = 400
	ShowNotes      = 5
)

func ValidateReason(reason, seedID string) error {
	if n := utf8.RuneCountInString(strings.TrimSpace(reason)); n > MaxReasonChars {
		return fmt.Errorf("that reason is %d characters and the limit is %d; the detail belongs on the log (`attn seed note %s -m …`)", n, MaxReasonChars, seedID)
	}
	return nil
}

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

func NewNoteID() (string, error) { return shortid.New(noteIDPrefix) }

func (n Note) Encode() ([]byte, error) { return json.Marshal(n) }

func DecodeNote(body []byte) (Note, error) {
	var note Note
	if err := json.Unmarshal(body, &note); err != nil {
		return Note{}, fmt.Errorf("this note's stored body is not readable: %w", err)
	}
	return note, nil
}
