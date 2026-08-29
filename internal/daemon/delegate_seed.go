package daemon

import (
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/garden"
)

// A bind failure is logged, not fatal — except when the delegation named a
// crown, which must not launch silently unaimed.
func (d *Daemon) bindDelegationSeed(sessionID, plannerSessionID, brief, name, crown, cwd, agent string, fromChief bool) (string, error) {
	seedID, err := d.bindDelegatedSeed(sessionID, plannerSessionID, brief, name, crown, cwd, agent, fromChief)
	switch {
	case err == nil:
		d.logf("delegate: bound seed %q to session %s", seedID, sessionID)
	case crown != "" && !delegationSeedUnavailable(err):
		return "", fmt.Errorf("dispatch %s at %s: %w", sessionID, crown, err)
	case delegationSeedUnavailable(err):
		d.logf("delegate: no seed bound to session %s: %v", sessionID, err)
	default:
		d.logf("delegate: binding a seed to session %s failed: %v", sessionID, err)
	}
	return seedID, nil
}

// Idempotent through the dispatch record: a delegation resumed after a daemon
// crash re-binds instead of planting a second seed.
func (d *Daemon) bindDelegatedSeed(sessionID, plannerSessionID, brief, name, crown, cwd, agent string, fromChief bool) (string, error) {
	if err := d.requireHome(garden.Surface); err != nil {
		return "", err
	}
	if bound, ok := d.gardenDispatchCrown(sessionID); ok {
		return bound, nil
	}
	seedID := strings.TrimSpace(crown)
	if seedID == "" {
		seed, err := d.plantDelegatedSeed(sessionID, plannerSessionID, brief, name)
		if err != nil {
			return "", err
		}
		seedID = seed.ID
	} else if err := d.tendDispatchedSeed(sessionID, plannerSessionID, seedID); err != nil {
		return "", err
	}
	if err := d.recordGardenDispatch(sessionID, seedID, plannerSessionID, cwd, agent, fromChief); err != nil {
		return "", fmt.Errorf("bind %s to session %s: %w", seedID, sessionID, err)
	}
	d.ringSeedActivity(seedID, gardenRingEvents[garden.VerbTend], sessionID, plannerSessionID)
	return seedID, nil
}

// Planted already tended by its delegate: a seed that exists unheld for a moment
// is one `ready` can offer away.
func (d *Daemon) plantDelegatedSeed(sessionID, plannerSessionID, brief, name string) (garden.Seed, error) {
	title := strings.TrimSpace(name)
	if title == "" {
		title = "delegated work"
	}
	body := strings.TrimSpace(brief)
	if err := garden.ValidatePlant(title, body); err != nil {
		return garden.Seed{}, err
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return garden.Seed{}, err
	}
	seed := garden.Seed{
		Title:          title,
		Body:           body,
		Status:         garden.StatusPlanted,
		StepSlug:       garden.StepSlug(title),
		PlanterSession: plannerSessionID,
		PlanterMember:  d.resolveTenderMember("", plannerSessionID),
		Edges:          []garden.Edge{},
		Vars:           []garden.Var{},
	}
	if parent, ok := d.gardenDispatchCrown(plannerSessionID); ok && parent != "" {
		seed.Edges = append(seed.Edges, garden.Edge{Kind: garden.EdgePartOf, To: parent})
	}
	tender := garden.Tender{Session: sessionID}
	// Nothing holds an unwritten seed: the liveness predicate is never consulted.
	seed, err = garden.Transition(seed, garden.VerbTend, garden.Ask{Actor: tender}, func(string) bool { return false })
	if err != nil {
		return garden.Seed{}, err
	}
	seed, _, err = d.mintAndPlant(*schema, seed)
	return seed, err
}

func delegationSeedUnavailable(err error) bool {
	var fenced *enrollment.FencedError
	return errors.As(err, &fenced)
}

// validateDispatchCrown already refused a seed held by a live session; this
// take-over through garden.Transition is the race backstop behind it.
func (d *Daemon) tendDispatchedSeed(sessionID, plannerSessionID, seedID string) error {
	actor := garden.Tender{Session: sessionID, Member: d.resolveTenderMember("", sessionID)}
	if _, _, err := d.applySeedTransitionAs(seedID, garden.VerbTend, garden.Ask{Actor: actor}, d.dispatchSessionLive(plannerSessionID)); err != nil {
		return fmt.Errorf("tend %s as session %s: %w", seedID, sessionID, err)
	}
	return nil
}

// Every session as it really is, except the delegating one, which is handing the
// seed over.
func (d *Daemon) dispatchSessionLive(plannerSessionID string) func(string) bool {
	planner := strings.TrimSpace(plannerSessionID)
	return func(sessionID string) bool {
		if planner != "" && strings.TrimSpace(sessionID) == planner {
			return false
		}
		return d.sessionExists(sessionID)
	}
}
