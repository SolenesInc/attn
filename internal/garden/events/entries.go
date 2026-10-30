package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/garden"
)

type entryShape uint8

const (
	moveEntry entryShape = iota + 1
	editEntry
	linkEntries
	noEntry
)

type EntryRule struct {
	event  *definition
	shape  entryShape
	words  string
	linked bool
	none   string
}

func Move[T any](event EventType[T], words string) EntryRule {
	return EntryRule{event: event.def, shape: moveEntry, words: words}
}

func Edit[T any](event EventType[T]) EntryRule {
	return EntryRule{event: event.def, shape: editEntry}
}

func Link(event EventType[EdgePayload], linked bool) EntryRule {
	return EntryRule{event: event.def, shape: linkEntries, linked: linked}
}

func NoEntry[T any](event EventType[T], why string) EntryRule {
	return EntryRule{event: event.def, shape: noEntry, none: why}
}

type EntryRules struct{ rules []EntryRule }

func Entries(rules ...EntryRule) EntryRules {
	return EntryRules{rules: append([]EntryRule(nil), rules...)}
}

func (m *Model) bindEntryRules(catalog EventCatalog, entries EntryRules) error {
	for _, rule := range entries.rules {
		event, exists := m.compiled.byHandle[rule.event]
		if !exists {
			return fmt.Errorf("log entry rule references an event handle outside the catalog")
		}
		if event.entry != nil {
			return fmt.Errorf("%s: duplicate log entry rule", event.def.name)
		}
		if rule.shape == noEntry && strings.TrimSpace(rule.none) == "" {
			return fmt.Errorf("%s: a rule that writes no log entry says why", event.def.name)
		}
		bound := rule
		event.entry = &bound
	}
	for _, def := range catalog.events {
		if m.compiled.byHandle[def].entry == nil {
			return fmt.Errorf("%s: missing log entry rule; declare the entry it writes, or NoEntry with why", def.name)
		}
	}
	return nil
}

type Entry struct {
	Seed string
	Kind string
	Body string
}

func (m *Model) Entries(occurrence Occurrence, detail string) ([]Entry, error) {
	if m == nil || m.compiled == nil || occurrence.model != m.compiled || occurrence.event == nil {
		return nil, fmt.Errorf("occurrence must be bound to this validated model")
	}
	rule := occurrence.event.entry
	switch rule.shape {
	case noEntry:
		return nil, nil
	case moveEntry:
		body := garden.MoveEntryBody(rule.words, strings.TrimSpace(detail))
		if body == "" {
			return nil, nil
		}
		return []Entry{{Seed: occurrence.subject, Kind: garden.NoteKindMove, Body: body}}, nil
	case editEntry:
		return []Entry{{Seed: occurrence.subject, Kind: garden.NoteKindEdit, Body: detail}}, nil
	case linkEntries:
		var edge EdgePayload
		if err := json.Unmarshal(occurrence.payload, &edge); err != nil {
			return nil, err
		}
		onFrom, onTo := garden.LinkEntryBodies(edge.EdgeKind, occurrence.subject, edge.TargetSeedID, rule.linked)
		return []Entry{
			{Seed: occurrence.subject, Kind: garden.NoteKindLink, Body: onFrom},
			{Seed: edge.TargetSeedID, Kind: garden.NoteKindLink, Body: onTo},
		}, nil
	default:
		return nil, fmt.Errorf("%s: unsupported log entry rule", occurrence.event.def.name)
	}
}
