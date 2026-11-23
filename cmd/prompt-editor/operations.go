package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/prompts"
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$`)

type operationError struct {
	Code    string `json:"code"`
	Message string `json:"error"`
	Path    string `json:"path,omitempty"`
	Current any    `json:"current,omitempty"`
}

func (e *operationError) Error() string { return e.Message }
func conflict(message string, current any) error {
	return &operationError{Code: "revision_conflict", Message: message, Current: current}
}

func decodeJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}

type operationRequest struct {
	Op        string            `json:"op"`
	ID        string            `json:"id,omitempty"`
	Title     string            `json:"title,omitempty"`
	Event     string            `json:"event,omitempty"`
	Scenario  string            `json:"scenario,omitempty"`
	Situation string            `json:"situation,omitempty"`
	Path      string            `json:"path,omitempty"`
	Expect    string            `json:"expect,omitempty"`
	Values    prompts.Values    `json:"values,omitempty"`
	Inputs    map[string]string `json:"inputs,omitempty"`
	Base      string            `json:"base,omitempty"`
	Mode      string            `json:"mode,omitempty"`
	Target    string            `json:"target,omitempty"`
	Include   []string          `json:"include,omitempty"`
}

func eventParts(key string) (string, string, error) {
	r, event, ok := strings.Cut(key, "/")
	if !ok || r == "" || event == "" {
		return "", "", fmt.Errorf("event must be recipient/event")
	}
	return r, event, nil
}

func (e *editor) operation(ctx context.Context, q operationRequest) (any, error) {
	switch q.Op {
	case "authoring":
		return map[string]any{"guide": "docs/internal/prompt-authoring.md", "workflow": authoringWorkflow}, nil
	case "refresh":
		return e.refresh(ctx)
	case "scenarios":
		return e.scenarios()
	case "situations":
		return e.situations()
	case "scenario-save":
		return e.saveScenario(q)
	}
	snapshot, err := e.snapshot()
	if err != nil {
		return nil, err
	}
	if q.Op == "catalog" {
		v, err := snapshotView(snapshot)
		if err == nil {
			if freshness := e.freshness(snapshot); freshness != nil {
				v.Validation = freshness.Error()
			}
		}
		return v, err
	}
	if err := e.freshness(snapshot); err != nil {
		return nil, err
	}
	catalog, err := snapshot.load(nil)
	if err != nil {
		return nil, err
	}
	if q.Op == "uses" {
		return findUsages(catalog, q.Target), nil
	}
	if q.Op == "list" {
		items := []map[string]string{}
		for _, r := range catalog.Recipients() {
			for _, event := range r.Events {
				items = append(items, map[string]string{"event": r.ID + "/" + event.ID, "delivery": event.Delivery, "description": event.Description})
			}
		}
		return items, nil
	}
	scenarios, err := e.scenarios()
	if err != nil {
		return nil, err
	}
	if q.Op == "context" {
		return e.authoringContext(ctx, q, snapshot, catalog, scenarios)
	}
	if q.Op == "check" || q.Op == "compare" {
		return e.checkScenarios(ctx, q, snapshot, catalog, scenarios)
	}
	if q.Op != "inspect" {
		return nil, fmt.Errorf("unknown operation %q", q.Op)
	}
	key, values := q.Event, q.Values
	if q.Scenario != "" {
		s, ok := scenarios[q.Scenario]
		if !ok {
			return nil, fmt.Errorf("unknown scenario %s", q.Scenario)
		}
		key = s.Recipient + "/" + s.Event
		values, _, err = scenarioValues(catalog, scenarios, q.Scenario, map[string]bool{})
		if err != nil {
			return nil, err
		}
	}
	r, eventID, err := eventParts(key)
	if err != nil {
		return nil, err
	}
	fields, err := catalog.Fields(r, eventID)
	if err != nil {
		return nil, err
	}
	var event prompts.Event
	for _, recipient := range catalog.Recipients() {
		if recipient.ID == r {
			for _, candidate := range recipient.Events {
				if candidate.ID == eventID {
					event = candidate
				}
			}
		}
	}
	sources := map[string]source{}
	visitNode(event.Body, func(n prompts.Node) {
		if n.Source != "" {
			sources[n.Source] = snapshot.Sources[n.Source]
		}
	})
	result := map[string]any{"event": key, "definition": event, "fields": fields, "sources": sources, "declarations": e.declarations(event), "catalog_revision": revision(snapshot.Manifest)}
	if values != nil {
		rendered, err := catalog.Render(r, eventID, values)
		if err != nil {
			return nil, err
		}
		result["result"] = rendered
		result["values"] = values
	}
	return result, nil
}

type situationCheck struct {
	ID    string `json:"id"`
	Error string `json:"error,omitempty"`
}

type scenarioCheck struct {
	ID      string          `json:"id"`
	Event   string          `json:"event"`
	Changed bool            `json:"changed"`
	Error   string          `json:"error,omitempty"`
	Current *prompts.Result `json:"current,omitempty"`
	Base    *prompts.Result `json:"base,omitempty"`
	Diff    string          `json:"diff,omitempty"`
}

func (e *editor) checkScenarios(ctx context.Context, q operationRequest, snapshot catalogSnapshot, catalog *prompts.Catalog, scenarios map[string]scenario) (any, error) {
	var base *baseSelection
	var err error
	if q.Op == "compare" {
		if q.Base == "" {
			return nil, fmt.Errorf("compare requires --base")
		}
		if q.Mode == "" {
			q.Mode = "merge-base"
		}
		base, err = e.selectBase(ctx, q.Base, q.Mode)
		if err != nil {
			return nil, err
		}
	}
	checks := []scenarioCheck{}
	names := []string{}
	for name := range scenarios {
		if q.Scenario == "" || q.Scenario == name {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if q.Scenario != "" && len(names) == 0 {
		return nil, fmt.Errorf("unknown scenario %s", q.Scenario)
	}
	valid := true
	for _, name := range names {
		s := scenarios[name]
		check := scenarioCheck{ID: name, Event: s.Recipient + "/" + s.Event}
		values, _, err := scenarioValues(catalog, scenarios, name, map[string]bool{})
		if err == nil {
			var rendered prompts.Result
			rendered, err = catalog.Render(s.Recipient, s.Event, values)
			if err == nil {
				check.Current = &rendered
			}
		}
		if err != nil {
			check.Error = err.Error()
			valid = false
		}
		if base != nil && base.catalog != nil {
			baseValues, _, baseErr := scenarioValues(base.catalog, scenarios, name, map[string]bool{})
			before := renderSide(base.catalog, s.Recipient, s.Event, baseValues)
			if baseErr != nil && before.Status != "absent" {
				before.Error = baseErr.Error()
			}
			check.Base = before.Result
			if before.Error != "" {
				check.Error = "base: " + before.Error
				valid = false
			}
			if check.Current != nil {
				text := ""
				if before.Result != nil {
					text = before.Result.Text
				}
				check.Changed = before.Result == nil || text != check.Current.Text || before.Result.Delivery != check.Current.Delivery
				check.Diff, err = unifiedDiff(ctx, text, check.Current.Text)
				if err != nil {
					return nil, err
				}
			}
		}
		checks = append(checks, check)
	}
	situations, err := e.situations()
	if err != nil {
		return nil, err
	}
	situationChecks := []situationCheck{}
	for _, id := range slices.Sorted(maps.Keys(situations)) {
		check := situationCheck{ID: id}
		if err := situations[id].missingScenario(scenarios); err != nil {
			check.Error = err.Error()
			valid = false
		}
		situationChecks = append(situationChecks, check)
	}
	result := map[string]any{"valid": valid, "scenarios": checks, "situations": situationChecks, "catalog_revision": revision(snapshot.Manifest)}
	if base != nil {
		result["base_commit"] = base.Commit
		result["unavailable"] = base.Unavailable
		changed := []string{}
		for name, current := range snapshot.Sources {
			if before, ok := base.Sources[name]; !ok || current.Text != before.Text {
				changed = append(changed, name)
			}
		}
		for name := range base.Sources {
			if _, ok := snapshot.Sources[name]; !ok {
				changed = append(changed, name)
			}
		}
		sort.Strings(changed)
		result["sources"] = changed
		impact := map[string]string{}
		for _, path := range changed {
			for _, u := range findUsages(catalog, path) {
				impact[u.Event] = u.Via
			}
			if base.catalog != nil {
				for _, u := range findUsages(base.catalog, path) {
					impact[u.Event] = u.Via
				}
			}
		}
		if base.catalog != nil {
			for _, r := range catalog.Recipients() {
				for _, event := range r.Events {
					key := r.ID + "/" + event.ID
					before := ""
					for _, br := range base.Recipients {
						if br.ID == r.ID {
							for _, be := range br.Events {
								if be.ID == event.ID {
									data, _ := json.Marshal(be)
									before = string(data)
								}
							}
						}
					}
					data, _ := json.Marshal(event)
					if before != string(data) {
						impact[key] = "definition"
					}
				}
			}
		}
		if base.catalog != nil {
			for _, r := range base.Recipients {
				for _, event := range r.Events {
					if _, err := catalog.Fields(r.ID, event.ID); err != nil {
						impact[r.ID+"/"+event.ID] = "removed"
					}
				}
			}
		}
		result["affected_events"] = impact
	}
	return result, nil
}

func (e *editor) saveScenario(q operationRequest) (any, error) {
	if !validName.MatchString(q.ID) {
		return nil, fmt.Errorf("scenario id must contain letters, digits, underscores or hyphens")
	}
	r, event, err := eventParts(q.Event)
	if err != nil {
		return nil, err
	}
	s := scenario{Version: 1, ID: q.ID, Description: q.Title, Recipient: r, Event: event, Values: q.Values, Inputs: q.Inputs}
	snapshot, err := e.snapshot()
	if err != nil {
		return nil, err
	}
	catalog, err := snapshot.load(nil)
	if err != nil {
		return nil, err
	}
	all, err := e.scenarios()
	if err != nil {
		return nil, err
	}
	all[s.ID] = s
	values, _, err := scenarioValues(catalog, all, s.ID, map[string]bool{})
	if err != nil {
		return nil, err
	}
	if _, err := catalog.Render(r, event, values); err != nil {
		return nil, err
	}
	name := "scenarios/" + s.ID + ".json"
	previous, err := e.root.ReadFile(name)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil && revision(previous) != q.Expect {
		return nil, conflict("scenario changed; reload before saving", revision(previous))
	}
	if os.IsNotExist(err) && q.Expect != "" {
		return nil, conflict("scenario was removed", nil)
	}
	if err := e.root.MkdirAll("scenarios", 0755); err != nil {
		return nil, err
	}
	if err := writeJSON(e.root, name, s); err != nil {
		return nil, err
	}
	data, err := e.root.ReadFile(name)
	if err != nil {
		return nil, err
	}
	s.Revision = revision(data)
	return s, nil
}
