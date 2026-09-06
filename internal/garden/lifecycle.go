package garden

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/crew"
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

type Tender struct {
	Session string
	Member  string
}

func (t Tender) Name() string {
	if member := strings.TrimSpace(t.Member); member != "" {
		return member
	}
	return strings.TrimSpace(t.Session)
}

func (t Tender) DisplayName() string { return crew.HolderName(t.Member, t.Session) }

func (t Tender) Named() bool { return t.Name() != "" }

// A terminal pane runs with no ATTN_SESSION_ID (internal/pty/manager.go strips
// it), so comparing sessions alone would hand a live claim to whoever asked next.
func (t Tender) Is(other Tender) bool {
	mine, theirs := strings.TrimSpace(t.Session), strings.TrimSpace(other.Session)
	if mine != "" && theirs != "" {
		return mine == theirs
	}
	return mine == theirs && strings.TrimSpace(t.Member) == strings.TrimSpace(other.Member)
}

func (t Tender) Holds(sessionLive func(sessionID string) bool) bool {
	if !t.Named() {
		return false
	}
	if session := strings.TrimSpace(t.Session); session != "" {
		return sessionLive(session)
	}
	return true
}

type Ask struct {
	Actor  Tender
	Reason string
	Force  bool
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

func Transition(seed Seed, verb Verb, ask Ask, sessionLive func(sessionID string) bool) (Seed, error) {
	rule, ok := moves[verb]
	if !ok {
		return Seed{}, fmt.Errorf("%q is not something a seed does", verb)
	}
	reason := strings.TrimSpace(ask.Reason)

	if !slices.Contains(rule.from, seed.Status) {
		return Seed{}, refuseState(seed, verb, rule)
	}
	if rule.claims && !ask.Actor.Named() {
		return Seed{}, fmt.Errorf(
			"tending %s records who holds it and this call named nobody; run it from an attn session, or pass --member <name>", seed.ID)
	}
	if held := seed.Tender(); held.Holds(sessionLive) && !held.Is(ask.Actor) && !ask.Force {
		return Seed{}, refuseTakeover(seed, verb, held)
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
	switch {
	case rule.claims:
		next.TenderSession = strings.TrimSpace(ask.Actor.Session)
		next.TenderMember = strings.TrimSpace(ask.Actor.Member)
	default:
		next.TenderSession = ""
		next.TenderMember = ""
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
	return next, nil
}

func (s Seed) Tender() Tender {
	return Tender{Session: s.TenderSession, Member: s.TenderMember}
}

func (d Dispatch) Dispatcher() Tender {
	return Tender{Session: d.DispatcherSession, Member: d.DispatcherMember}
}

func refuseTakeover(seed Seed, verb Verb, held Tender) error {
	return fmt.Errorf(
		"%s is being tended by %s, and `attn seed %s` takes it from them.\n"+
			"Pass --force to act anyway; the log will record it. Or say what you need on the log: attn seed note %s -m \"…\"",
		seed.ID, held.DisplayName(), verb, seed.ID)
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
	ID            string             `json:"id"`
	Seed          string             `json:"seed"`
	Kind          string             `json:"kind"`
	Body          string             `json:"body"`
	AuthorSession string             `json:"author_session"`
	AuthorMember  string             `json:"author_member"`
	Artifact      *ArtifactReference `json:"artifact,omitempty"`
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

// MaxNoteBytes is a tripwire: the longest production description on 2026-08-12 was 14,920
// chars. It must fit the 64KiB socket frame, which JSON escaping inflates (45KB -> 75KB).
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

func (n Note) Author() Tender {
	return Tender{Session: n.AuthorSession, Member: n.AuthorMember}
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
