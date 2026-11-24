package garden

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/shortid"
)

const (
	CollectionBeds = "beds"

	bedIDPrefix = "b-"

	MaxBedNameChars = 60
	MaxBedNoteChars = 200
	MaxBedRepos     = 32
	MaxPulseChars   = 160
)

var BedVerbs = []string{"ls", "add", "set", "rm", "assign"}

var reservedBedNames = []string{"all", "unassigned", "automations"}

type Bed struct {
	ID        string   `json:"id"`
	ProfileID string   `json:"profile_id"`
	Name      string   `json:"name"`
	Slug      string   `json:"slug"`
	Note      string   `json:"note,omitempty"`
	Repos     []string `json:"repos,omitempty"`
}

func BedsSchema() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace:  Namespace,
		Collection: CollectionBeds,
		Fields: []docstore.FieldSpec{
			{Name: "profile_id", Type: docstore.FieldString},
			{Name: "slug", Type: docstore.FieldString},
		},
	}
}

func NewBedID() (string, error) { return shortid.New(bedIDPrefix) }

func (b Bed) Encode() ([]byte, error) {
	return json.Marshal(b)
}

func DecodeBed(body []byte) (Bed, error) {
	var bed Bed
	if err := json.Unmarshal(body, &bed); err != nil {
		return Bed{}, fmt.Errorf("this bed's stored body is not readable: %w", err)
	}
	return bed, nil
}

func BedSlug(name string) string {
	var words []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			words = append(words, b.String())
			b.Reset()
		}
	}
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	slug := strings.Join(words, "-")
	if runes := []rune(slug); len(runes) > MaxBedNameChars {
		slug = strings.Trim(string(runes[:MaxBedNameChars]), "-")
	}
	if slug == "" {
		return "bed"
	}
	return slug
}

func ValidateBedName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", protocol.Refuse(protocol.RefusalInvalidInput, "a bed needs a name: {attn seed bed add \"<name>\"}")
	}
	if strings.ContainsAny(name, "\n\r") {
		return "", protocol.Refuse(protocol.RefusalInvalidInput, "a bed name must be one line")
	}
	if n := utf8.RuneCountInString(name); n > MaxBedNameChars {
		return "", protocol.Refuse(protocol.RefusalLimit, "that bed name is %d characters and the limit is %d", n, MaxBedNameChars)
	}
	lower := strings.ToLower(name)
	if slices.Contains(reservedBedNames, lower) || slices.Contains(reservedBedNames, BedSlug(name)) {
		return "", protocol.Refuse(protocol.RefusalInvalidInput, "%q is a reserved garden view name; choose another bed name", name)
	}
	return name, nil
}

func ValidateBedNote(raw string) (string, error) {
	note := strings.TrimSpace(raw)
	if note == "" {
		return "", nil
	}
	if strings.ContainsAny(note, "\n\r") {
		return "", protocol.Refuse(protocol.RefusalInvalidInput, "a bed note must be one line")
	}
	if n := utf8.RuneCountInString(note); n > MaxBedNoteChars {
		return "", protocol.Refuse(protocol.RefusalLimit, "that bed note is %d characters and the limit is %d", n, MaxBedNoteChars)
	}
	return note, nil
}

func RepoKey(repo string) string {
	trimmed := strings.TrimSpace(repo)
	if strings.Contains(trimmed, "/") {
		return cleanRepoPath(trimmed)
	}
	return strings.ToLower(trimmed)
}

func cleanRepoPath(raw string) string {
	if rest, ok := strings.CutPrefix(raw, "~/"); ok {
		cleaned := filepath.Clean(rest)
		if cleaned == "." {
			return "~"
		}
		return "~/" + cleaned
	}
	return filepath.Clean(raw)
}

func NormalizeBedRepos(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, entry := range raw {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			return nil, protocol.Refuse(protocol.RefusalInvalidInput, "a bed repo cannot be empty")
		}
		if strings.ContainsAny(trimmed, "\n\r") {
			return nil, protocol.Refuse(protocol.RefusalInvalidInput, "a bed repo must be one line")
		}
		if strings.Contains(trimmed, "/") {
			trimmed = cleanRepoPath(trimmed)
		}
		key := RepoKey(trimmed)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, trimmed)
	}
	if len(out) > MaxBedRepos {
		return nil, protocol.Refuse(protocol.RefusalLimit, "a bed holds at most %d repos, asked for %d", MaxBedRepos, len(out))
	}
	return out, nil
}

func FindBed(beds []Bed, ref string) (Bed, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Bed{}, false
	}
	for _, bed := range beds {
		if bed.ID == ref {
			return bed, true
		}
	}
	for _, bed := range beds {
		if strings.EqualFold(bed.Name, ref) {
			return bed, true
		}
	}
	for _, bed := range beds {
		if strings.EqualFold(bed.Slug, ref) {
			return bed, true
		}
	}
	return Bed{}, false
}

func MatchBedForRepo(beds []Bed, repoPath string) (Bed, bool) {
	cleaned := strings.TrimSpace(repoPath)
	if cleaned == "" {
		return Bed{}, false
	}
	cleaned = cleanRepoPath(cleaned)
	if cleaned == "." {
		return Bed{}, false
	}
	var pathMatch Bed
	bestLen := -1
	for _, bed := range beds {
		for _, repo := range bed.Repos {
			if !strings.Contains(repo, "/") {
				continue
			}
			target := cleanRepoPath(repo)
			if cleaned == target || (target != "/" && strings.HasPrefix(cleaned, target+"/")) {
				if len(target) > bestLen || (len(target) == bestLen && bed.ID < pathMatch.ID) {
					pathMatch, bestLen = bed, len(target)
				}
			}
		}
	}
	if bestLen >= 0 {
		return pathMatch, true
	}
	base := filepath.Base(cleaned)
	if base == "" || base == "." || base == "/" {
		return Bed{}, false
	}
	var nameMatch Bed
	hasName := false
	for _, bed := range beds {
		for _, repo := range bed.Repos {
			if !strings.Contains(repo, "/") && strings.EqualFold(repo, base) {
				if !hasName || bed.ID < nameMatch.ID {
					nameMatch, hasName = bed, true
				}
			}
		}
	}
	return nameMatch, hasName
}

func EffectiveBedID(seed Seed, plots PlotTree) string {
	cur := seed
	seen := map[string]bool{cur.ID: true}
	for {
		parentID, ok := ParentOf(cur)
		if !ok || seen[parentID] {
			return cur.BedID
		}
		parent, found := plots.Seed(parentID)
		if !found {
			return cur.BedID
		}
		seen[parentID] = true
		cur = parent
	}
}

func ValidatePulse(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", protocol.Refuse(protocol.RefusalInvalidInput, "a pulse needs one line saying where the work stands: {attn seed pulse <id> -m \"<one-line status>\"}")
	}
	if strings.ContainsAny(text, "\n\r") {
		return "", protocol.Refuse(protocol.RefusalInvalidInput, "a pulse is one line; put longer detail on the log with {attn seed note <id> -m \"…\"}")
	}
	if n := utf8.RuneCountInString(text); n > MaxPulseChars {
		return "", protocol.Refuse(protocol.RefusalLimit, "that pulse is %d characters and the limit is %d; put longer detail on the log with {attn seed note <id> -m \"…\"}", n, MaxPulseChars)
	}
	return text, nil
}
