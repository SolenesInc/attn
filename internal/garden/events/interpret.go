package events

import (
	"fmt"
	"github.com/victorarias/attn/internal/who"
	"sort"

	"github.com/victorarias/attn/internal/garden"
)

type Interpreted struct {
	bellName string
	cause    who.Actor
	excluded []who.Party
}

func (i Interpreted) Quiet() bool      { return i.bellName == "" }
func (i Interpreted) BellName() string { return i.bellName }

func (m *Model) Interpret(name, seedID string, payload []byte) (Interpreted, error) {
	if m == nil || m.compiled == nil || !m.compiled.validated {
		return Interpreted{}, fmt.Errorf("interpretation requires a validated model")
	}
	if err := garden.ValidateID(seedID); err != nil {
		return Interpreted{}, err
	}
	event, exists := m.compiled.byName[name]
	if !exists {
		return Interpreted{}, fmt.Errorf("unknown seed event %q", name)
	}
	values, err := event.decode(payload)
	if err != nil {
		return Interpreted{}, err
	}
	var cause who.Actor
	if text := values["caused_by"].(string); text != "" {
		cause, err = who.ParseActor(text)
		if err != nil {
			return Interpreted{}, err
		}
	}
	var notified who.Party
	if value, exists := values["directly_notified"]; exists && value.(string) != "" {
		notified, err = who.ParseParty(value.(string))
		if err != nil {
			return Interpreted{}, err
		}
	}
	decision := event.decision
	for decision.kind == chooseAction {
		if values[decision.condition.field].(bool) {
			decision = *decision.yes
		} else {
			decision = *decision.no
		}
	}
	if decision.kind == quietAction {
		return Interpreted{cause: cause}, nil
	}
	if decision.kind != ringAction {
		return Interpreted{}, fmt.Errorf("%s: unsupported compiled decision", name)
	}
	interpreted := Interpreted{bellName: decision.bell.name, cause: cause}
	for _, exclusion := range decision.bell.exclusions {
		var party who.Party
		switch exclusion {
		case PartyThatCausedTheEvent:
			party, _ = cause.Party()
		case PartyNotifiedDirectly:
			party = notified
		}
		if !party.IsZero() {
			interpreted.excluded = append(interpreted.excluded, party)
		}
	}
	return interpreted, nil
}

type RoleResolver interface {
	ResolveSeedRole(seedID string, role Role) ([]who.Party, error)
}

func (m *Model) Recipients(seedID string, decision Interpreted, resolver RoleResolver) ([]who.Party, error) {
	if decision.Quiet() {
		return nil, nil
	}
	bell, exists := m.compiled.bells[decision.bellName]
	if !exists {
		return nil, fmt.Errorf("unknown bell definition %q", decision.bellName)
	}
	recipients, err := resolveAudience(seedID, bell.audience, resolver)
	if err != nil {
		return nil, err
	}
	if len(decision.excluded) == 0 {
		return recipients, nil
	}
	excluded := map[who.Party]bool{}
	for _, party := range decision.excluded {
		excluded[party] = true
	}
	out := recipients[:0]
	for _, recipient := range recipients {
		if !excluded[recipient] {
			out = append(out, recipient)
		}
	}
	return out, nil
}

func (m *Model) RecipientEligible(bellName, seedID string, recipient who.Address, resolver RoleResolver) (bool, error) {
	if m == nil || m.compiled == nil || !m.compiled.validated {
		return false, fmt.Errorf("eligibility requires a validated model")
	}
	bell, exists := m.compiled.bells[bellName]
	if !exists {
		return false, fmt.Errorf("pending bell %q has no retained definition", bellName)
	}
	current, err := resolveAudience(seedID, bell.retention, resolver)
	if err != nil {
		return false, err
	}
	for _, sessionID := range current {
		if sessionID.Address() == recipient {
			return true, nil
		}
	}
	return false, nil
}

func resolveAudience(seedID string, audience AudienceDef, resolver RoleResolver) ([]who.Party, error) {
	if resolver == nil {
		return nil, fmt.Errorf("audience %q requires a role resolver", audience.name)
	}
	resolved := map[who.Party]bool{}
	for _, role := range audience.roles {
		sessions, err := resolver.ResolveSeedRole(seedID, role)
		if err != nil {
			return nil, fmt.Errorf("resolve audience %q role %d: %w", audience.name, role, err)
		}
		for _, sessionID := range sessions {
			if !sessionID.IsZero() {
				resolved[sessionID] = true
			}
		}
	}
	sessions := make([]who.Party, 0, len(resolved))
	for sessionID := range resolved {
		sessions = append(sessions, sessionID)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].String() < sessions[j].String() })
	return sessions, nil
}
