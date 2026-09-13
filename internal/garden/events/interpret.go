package events

import (
	"fmt"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/garden"
)

type Interpreted struct {
	bellName string
	cause    string
}

func (i Interpreted) Quiet() bool               { return i.bellName == "" }
func (i Interpreted) BellName() string          { return i.bellName }
func (i Interpreted) CausedBySessionID() string { return i.cause }

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
	decision := event.decision
	for decision.kind == chooseAction {
		if values[decision.condition.field].(bool) {
			decision = *decision.yes
		} else {
			decision = *decision.no
		}
	}
	if decision.kind == quietAction {
		return Interpreted{cause: values["caused_by_session_id"].(string)}, nil
	}
	if decision.kind != ringAction {
		return Interpreted{}, fmt.Errorf("%s: unsupported compiled decision", name)
	}
	return Interpreted{bellName: decision.bell.name, cause: values["caused_by_session_id"].(string)}, nil
}

type RoleResolver interface {
	ResolveSeedRole(seedID string, role Role) ([]string, error)
}

func (m *Model) Recipients(seedID string, decision Interpreted, resolver RoleResolver) ([]string, error) {
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
	if decision.cause == "" {
		return recipients, nil
	}
	out := recipients[:0]
	for _, recipient := range recipients {
		if recipient != decision.cause {
			out = append(out, recipient)
		}
	}
	return out, nil
}

func (m *Model) RecipientEligible(bellName, seedID, recipient string, resolver RoleResolver) (bool, error) {
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
		if sessionID == recipient {
			return true, nil
		}
	}
	return false, nil
}

func resolveAudience(seedID string, audience AudienceDef, resolver RoleResolver) ([]string, error) {
	if resolver == nil {
		return nil, fmt.Errorf("audience %q requires a role resolver", audience.name)
	}
	resolved := map[string]bool{}
	for _, role := range audience.roles {
		sessions, err := resolver.ResolveSeedRole(seedID, role)
		if err != nil {
			return nil, fmt.Errorf("resolve audience %q role %d: %w", audience.name, role, err)
		}
		for _, sessionID := range sessions {
			if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
				resolved[sessionID] = true
			}
		}
	}
	sessions := make([]string, 0, len(resolved))
	for sessionID := range resolved {
		sessions = append(sessions, sessionID)
	}
	sort.Strings(sessions)
	return sessions, nil
}
