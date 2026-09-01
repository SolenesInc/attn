// docs/plans/2026-08-10-home-garden-crew-arc.md.
package garden

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
)

const Surface = "the garden"

const (
	Namespace             = "core/garden"
	CollectionSeeds       = "seeds"
	CollectionNotes       = "notes"
	CollectionDispatches  = "dispatches"
	CollectionReviewRuns  = "review-runs"
	CollectionReviewItems = "review-items"
)

const (
	StatusPlanted   = "planted"
	StatusGrowing   = "growing"
	StatusHarvested = "harvested"
	StatusWithered  = "withered"
	StatusDormant   = "dormant"
)

type Edge struct {
	Kind string `json:"kind"`
	To   string `json:"to"`
}

type Var struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Default     string   `json:"default,omitempty"`
	Pattern     string   `json:"pattern,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// Every declared field is written unconditionally, empty string and all: a field a query
// filters on must exist in every body, or `tender_session = ""` matches nothing.
type Seed struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Body            string `json:"body"`
	Status          string `json:"status"`
	StepSlug        string `json:"step_slug"`
	PlanterSession  string `json:"planter_session"`
	PlanterMember   string `json:"planter_member"`
	TenderSession   string `json:"tender_session"`
	TenderMember    string `json:"tender_member"`
	LastExecutionID string `json:"last_execution_id,omitempty"`
	StateChangedAt  string `json:"state_changed_at,omitempty"`
	Edges           []Edge `json:"edges"`
	Template        bool   `json:"template"`
	Gate            bool   `json:"gate"`
	Vars            []Var  `json:"vars"`
	Reason          string `json:"reason,omitempty"`
	ResumeSessionID string `json:"resume_session_id,omitempty"`
	ResumeCwd       string `json:"resume_cwd,omitempty"`
	ResumeAgent     string `json:"resume_agent,omitempty"`

	HarvestWhen *HarvestCondition `json:"harvest_when,omitempty"`
	// Flattened out of HarvestWhen by Encode: a docstore field is a top-level JSON
	// key, so armed seeds cannot be found through the nested object.
	HarvestWhenPullRequest string `json:"harvest_when_pull_request"`
}

// What a seed waits on before it harvests itself. The daemon settles it when the
// pull request merges; a pull request closed without merging clears it instead.
type HarvestCondition struct {
	// host:owner/repo#number, the session pull request id.
	PullRequest  string `json:"pull_request"`
	URL          string `json:"url"`
	SetAt        string `json:"set_at"`
	SetBySession string `json:"set_by_session,omitempty"`
	SetByMember  string `json:"set_by_member,omitempty"`
}

func ValidateHarvestCondition(c HarvestCondition) error {
	if strings.TrimSpace(c.PullRequest) == "" {
		return fmt.Errorf("a harvest condition needs the pull request it waits on, as host:owner/repo#number")
	}
	if strings.TrimSpace(c.URL) == "" {
		return fmt.Errorf("a harvest condition needs the pull request url, so the seed can point at what it waits on")
	}
	return nil
}

func SeedsSchema() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace:  Namespace,
		Collection: CollectionSeeds,
		Fields: []docstore.FieldSpec{
			{Name: "status", Type: docstore.FieldString},
			{Name: "step_slug", Type: docstore.FieldString},
			{Name: "tender_session", Type: docstore.FieldString},
			{Name: "harvest_when_pull_request", Type: docstore.FieldString},
			{Name: "template", Type: docstore.FieldBool},
			{Name: "gate", Type: docstore.FieldBool},
		},
	}
}

func NotesSchema() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace:  Namespace,
		Collection: CollectionNotes,
		Fields: []docstore.FieldSpec{
			{Name: "seed", Type: docstore.FieldString},
			{Name: "kind", Type: docstore.FieldString},
			{Name: "author_session", Type: docstore.FieldString},
			{Name: "author_member", Type: docstore.FieldString},
		},
	}
}

// `Crown` is scope inference, never a fence: the session may tend or plant
// anything, and who-holds-what stays the per-seed tender.
type Dispatch struct {
	SessionID         string `json:"session_id"`
	Crown             string `json:"crown"`
	DispatcherSession string `json:"dispatcher_session,omitempty"`
	Cwd               string `json:"cwd,omitempty"`
	Agent             string `json:"agent,omitempty"`
	HostKind          string `json:"host_kind,omitempty"`
	EndpointID        string `json:"endpoint_id,omitempty"`
	RepositoryRoot    string `json:"repository_root,omitempty"`
	RepositorySubdir  string `json:"repository_subdir,omitempty"`
	Branch            string `json:"branch,omitempty"`
	CapturedAt        string `json:"captured_at,omitempty"`
	SupersededBy      string `json:"superseded_by,omitempty"`
	OperationID       string `json:"operation_id,omitempty"`
	// Stays true through a role transfer: it says who dispatched, not who is chief.
	FromChief bool   `json:"from_chief,omitempty"`
	Resume    string `json:"resume,omitempty"`
}

const (
	HostLocal  = "local"
	HostRemote = "remote"
)

func DispatchesSchema() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace:  Namespace,
		Collection: CollectionDispatches,
		Fields: []docstore.FieldSpec{
			{Name: "crown", Type: docstore.FieldString},
		},
	}
}

func (d Dispatch) Encode() ([]byte, error) { return json.Marshal(d) }

func DecodeDispatch(body []byte) (Dispatch, error) {
	var dispatch Dispatch
	if err := json.Unmarshal(body, &dispatch); err != nil {
		return Dispatch{}, fmt.Errorf("this dispatch's stored body is not readable: %w", err)
	}
	return dispatch, nil
}

// Six characters of Crockford's base32 is 32^6 ~ 1.07e9; at ten thousand seeds a
// collision is ~4.7% likely, so the daemon mints again and planting writes create-only.
const (
	idPrefix     = "s-"
	noteIDPrefix = "n-"
	idBodyLen    = 6
	idAlphabet   = "0123456789abcdefghjkmnpqrstvwxyz"
	idAlphabetN  = 32
)

func NewID() (string, error) { return mintID(idPrefix) }

// 256 is a whole multiple of the 32-character alphabet, so the modulo is
// unbiased and no rejection loop is needed.
func mintID(prefix string) (string, error) {
	buf := make([]byte, idBodyLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mint %sid: %w", prefix, err)
	}
	out := make([]byte, 0, len(prefix)+idBodyLen)
	out = append(out, prefix...)
	for _, b := range buf {
		out = append(out, idAlphabet[int(b)%idAlphabetN])
	}
	return string(out), nil
}

func ValidateID(id string) error {
	body, ok := strings.CutPrefix(id, idPrefix)
	if !ok || len(body) != idBodyLen {
		return fmt.Errorf("%q is not a seed id: a seed id is %q followed by %d characters, like s-7k3f9m", id, idPrefix, idBodyLen)
	}
	for _, r := range body {
		if !strings.ContainsRune(idAlphabet, r) {
			return fmt.Errorf("%q is not a seed id: %q is not one of %s (i, l, o and u are left out so an id never misreads)", id, string(r), idAlphabet)
		}
	}
	return nil
}

// Tripwires. Measured 2026-08-12 against production ~/.attn: longest ticket title 81
// characters; the largest plan doc in this repo 75,843 bytes.
const (
	MaxTitleChars = 400
	MaxBodyBytes  = 1 << 20
	// Past the longest real title measured above, so it never truncates one.
	MaxSlugChars = 100
	// Enough to tell seeds apart, few enough to say out loud.
	MaxSlugWords = 6
)

func ValidatePlant(title, body string) error {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return fmt.Errorf("a seed needs a title: `attn seed plant \"what this is\"`")
	}
	if n := len([]rune(trimmed)); n > MaxTitleChars {
		return fmt.Errorf("that title is %d characters and the limit is %d; a title names the work in a line — the detail goes in the body (`-m`, or `-m -` to read stdin)", n, MaxTitleChars)
	}
	return ValidateBody(body)
}

func ValidateBody(body string) error {
	if n := len(body); n > MaxBodyBytes {
		return fmt.Errorf("max_body_bytes=%d, asked for %d; a seed's body is a plan, not an archive", MaxBodyBytes, n)
	}
	return nil
}

// A slug is how a seed is spoken of, not addressed: it names the seed in prose
// while the id names it in commands. Derived once at planting and then editable.
func StepSlug(title string) string {
	words := slugWords(title)
	kept := words[:0:0]
	for _, w := range words {
		if !slugStopWords[w] {
			kept = append(kept, w)
		}
	}
	// A title made only of stop words ("The One") still needs a name.
	if len(kept) == 0 {
		kept = words
	}
	if len(kept) > MaxSlugWords {
		kept = kept[:MaxSlugWords]
	}
	slug := strings.Join(kept, "-")
	if runes := []rune(slug); len(runes) > MaxSlugChars {
		slug = strings.Trim(string(runes[:MaxSlugChars]), "-")
	}
	if slug == "" {
		return "seed"
	}
	return slug
}

// Small on purpose: only the English filler that carries no meaning in a title.
var slugStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "in": true, "on": true, "at": true,
	"to": true, "for": true, "with": true, "by": true, "from": true, "and": true, "or": true,
	"as": true, "is": true, "into": true, "its": true, "it": true,
}

func slugWords(title string) []string {
	var words []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			words = append(words, b.String())
			b.Reset()
		}
	}
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return words
}

func (s Seed) Encode() ([]byte, error) {
	if s.Edges == nil {
		s.Edges = []Edge{}
	}
	if s.Vars == nil {
		s.Vars = []Var{}
	}
	s.HarvestWhenPullRequest = ""
	if s.HarvestWhen != nil {
		s.HarvestWhenPullRequest = s.HarvestWhen.PullRequest
	}
	return json.Marshal(s)
}

func Decode(body []byte) (Seed, error) {
	var seed Seed
	if err := json.Unmarshal(body, &seed); err != nil {
		return Seed{}, fmt.Errorf("this seed's stored body is not readable: %w", err)
	}
	return seed, nil
}

func ExportStamp(id string) string {
	return fmt.Sprintf("*generated from crown `%s` — edit the crown, not this file.*", id)
}

func Export(seed Seed) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", strings.TrimSpace(seed.Title))
	fmt.Fprintf(&b, "%s\n", ExportStamp(seed.ID))
	if body := strings.TrimRight(seed.Body, "\n"); body != "" {
		fmt.Fprintf(&b, "\n%s\n", body)
	}
	return b.String()
}
