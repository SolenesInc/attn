package garden

import (
	"fmt"
	"slices"
	"strings"
)

const (
	ArtifactMarkdownFile = "markdown_file"
	ArtifactNotebook     = "notebook"
	ArtifactRepository   = "repository"
	ArtifactURL          = "url"
)

var ArtifactKinds = []string{ArtifactMarkdownFile, ArtifactNotebook, ArtifactRepository, ArtifactURL}

// Optional fields are omitted rather than written empty: an empty string beside
// a set one reads as an answer.
type ArtifactReference struct {
	Kind               string `json:"kind"`
	NotebookDocumentID string `json:"notebook_document_id,omitempty"`
	Repository         string `json:"repository,omitempty"`
	Path               string `json:"path,omitempty"`
	URL                string `json:"url,omitempty"`
}

// A tripwire: the longest path in this repo is 84 characters.
const MaxArtifactFieldChars = 2048

func (a ArtifactReference) trimmed() ArtifactReference {
	return ArtifactReference{
		Kind:               strings.TrimSpace(strings.ToLower(a.Kind)),
		NotebookDocumentID: strings.TrimSpace(a.NotebookDocumentID),
		Repository:         strings.TrimSpace(a.Repository),
		Path:               strings.TrimSpace(a.Path),
		URL:                strings.TrimSpace(a.URL),
	}
}

func ValidateArtifact(raw ArtifactReference) (ArtifactReference, error) {
	a := raw.trimmed()
	if a.Kind == "" {
		return ArtifactReference{}, fmt.Errorf(
			"an artifact needs a kind; the kinds are %s", strings.Join(ArtifactKinds, ", "))
	}
	if !slices.Contains(ArtifactKinds, a.Kind) {
		return ArtifactReference{}, fmt.Errorf(
			"%q is not a kind of artifact; the kinds are %s", raw.Kind, strings.Join(ArtifactKinds, ", "))
	}
	for name, value := range map[string]string{
		"notebook_document_id": a.NotebookDocumentID,
		"repository":           a.Repository,
		"path":                 a.Path,
		"url":                  a.URL,
	} {
		if n := len(value); n > MaxArtifactFieldChars {
			return ArtifactReference{}, fmt.Errorf(
				"max_artifact_field_chars=%d, asked for %d on %s; a reference points at a document, it does not carry one",
				MaxArtifactFieldChars, n, name)
		}
	}
	required, allowed := artifactFields(a.Kind)
	for _, field := range required {
		if a.field(field) == "" {
			return ArtifactReference{}, fmt.Errorf(
				"a %s artifact needs %s", a.Kind, strings.Join(required, " and "))
		}
	}
	for _, field := range []string{"notebook_document_id", "repository", "path", "url"} {
		if a.field(field) != "" && !slices.Contains(allowed, field) {
			return ArtifactReference{}, fmt.Errorf(
				"a %s artifact carries %s, not %s", a.Kind, strings.Join(allowed, " and "), field)
		}
	}
	return a, nil
}

func artifactFields(kind string) (required, allowed []string) {
	switch kind {
	case ArtifactMarkdownFile:
		return []string{"path"}, []string{"path", "repository"}
	case ArtifactNotebook:
		return []string{"notebook_document_id"}, []string{"notebook_document_id"}
	case ArtifactRepository:
		return []string{"repository", "path"}, []string{"repository", "path"}
	case ArtifactURL:
		return []string{"url"}, []string{"url"}
	}
	return nil, nil
}

func (a ArtifactReference) field(name string) string {
	switch name {
	case "notebook_document_id":
		return a.NotebookDocumentID
	case "repository":
		return a.Repository
	case "path":
		return a.Path
	case "url":
		return a.URL
	}
	return ""
}

func (a ArtifactReference) Label() string {
	switch {
	case a.Path != "":
		return a.Path
	case a.NotebookDocumentID != "":
		return a.NotebookDocumentID
	case a.URL != "":
		return a.URL
	case a.Repository != "":
		return a.Repository
	}
	return a.Kind
}

func (a ArtifactReference) Identity() string {
	return strings.Join([]string{a.Kind, a.Path, a.NotebookDocumentID, a.Repository, a.URL}, "\x00")
}

func DefaultNoteBody(kind string, a ArtifactReference) string {
	verb := "attached"
	if kind == NoteKindDetach {
		verb = "detached"
	}
	if a.Repository != "" && a.Path != "" {
		return fmt.Sprintf("%s %s (%s)", verb, a.Path, a.Repository)
	}
	return fmt.Sprintf("%s %s", verb, a.Label())
}

func CurrentArtifacts(notes []Note) []ArtifactReference {
	current := map[string]ArtifactReference{}
	order := []string{}
	for i := len(notes) - 1; i >= 0; i-- {
		note := notes[i]
		if note.Artifact == nil {
			continue
		}
		key := note.Artifact.Identity()
		switch note.Kind {
		case NoteKindAttach:
			order = append(slices.DeleteFunc(order, func(held string) bool { return held == key }), key)
			current[key] = *note.Artifact
		case NoteKindDetach:
			delete(current, key)
		}
	}
	out := make([]ArtifactReference, 0, len(current))
	for _, key := range order {
		if artifact, held := current[key]; held {
			out = append(out, artifact)
		}
	}
	return out
}
