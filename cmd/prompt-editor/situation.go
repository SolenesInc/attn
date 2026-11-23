package main

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"slices"

	"github.com/victorarias/attn/internal/prompts"
)

type situation struct {
	Version     int      `json:"version"`
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Scenarios   []string `json:"scenarios"`
}

func (e *editor) situations() (map[string]situation, error) {
	names, err := fs.Glob(e.root.FS(), "situations/*.json")
	if err != nil {
		return nil, err
	}
	result := map[string]situation{}
	for _, name := range names {
		data, err := e.root.ReadFile(name)
		if err != nil {
			return nil, err
		}
		var s situation
		if err := decodeJSON(data, &s); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if s.Version != 1 || !validName.MatchString(s.ID) || name != "situations/"+s.ID+".json" {
			return nil, fmt.Errorf("%s: situation version must be 1 and id must match filename", name)
		}
		if len(s.Scenarios) == 0 {
			return nil, fmt.Errorf("%s: a situation lists at least one scenario", name)
		}
		result[s.ID] = s
	}
	return result, nil
}

func (s situation) missingScenario(scenarios map[string]scenario) error {
	for _, id := range s.Scenarios {
		if _, ok := scenarios[id]; !ok {
			return fmt.Errorf("situation %s names unknown scenario %s", s.ID, id)
		}
	}
	return nil
}

type pageRequest struct {
	Situation string         `json:"situation,omitempty"`
	Scenario  string         `json:"scenario,omitempty"`
	Event     string         `json:"event,omitempty"`
	Flips     prompts.Values `json:"flips,omitempty"`
	Base      string         `json:"base,omitempty"`
}

type pageEvent struct {
	Scenario   string               `json:"scenario,omitempty"`
	Event      string               `json:"event"`
	Definition prompts.Event        `json:"definition"`
	Trace      prompts.Trace        `json:"trace"`
	Values     prompts.Values       `json:"values"`
	Producers  map[string]pageEvent `json:"producers,omitempty"`
}

type pageSource struct {
	source
	Uses []usage `json:"uses"`
}

type pageResponse struct {
	Events     []pageEvent           `json:"events"`
	Sources    map[string]pageSource `json:"sources"`
	Base       map[string]string     `json:"base,omitempty"`
	BaseCommit string                `json:"base_commit,omitempty"`
	Stale      string                `json:"stale,omitempty"`
}

func renderPageEvent(c *prompts.Catalog, scenarios map[string]scenario, id string, visiting map[string]bool) (pageEvent, error) {
	values, producers, err := scenarioValues(c, scenarios, id, visiting)
	if err != nil {
		return pageEvent{}, err
	}
	s := scenarios[id]
	rendered, err := c.Render(s.Recipient, s.Event, values)
	if err != nil {
		return pageEvent{}, err
	}
	key := s.Recipient + "/" + s.Event
	return pageEvent{Scenario: id, Event: key, Definition: eventIndex(c)[key], Trace: rendered.Trace, Values: values, Producers: producers}, nil
}

func flipped(c *prompts.Catalog, scenarios map[string]scenario, flips prompts.Values) map[string]scenario {
	result := map[string]scenario{}
	for id, s := range scenarios {
		fields, err := c.Fields(s.Recipient, s.Event)
		if err != nil {
			result[id] = s
			continue
		}
		s.Values = maps.Clone(s.Values)
		if s.Values == nil {
			s.Values = prompts.Values{}
		}
		for _, field := range fields {
			if value, ok := flips[field.Name]; ok {
				if _, produced := s.Inputs[field.Name]; !produced {
					s.Values[field.Name] = value
				}
			}
		}
		result[id] = s
	}
	return result
}

const bareEvent = "@event"

func (e *editor) page(ctx context.Context, q pageRequest) (pageResponse, error) {
	asked := 0
	for _, name := range []string{q.Situation, q.Scenario, q.Event} {
		if name != "" {
			asked++
		}
	}
	if asked != 1 {
		return pageResponse{}, fmt.Errorf("a page shows one situation, scenario or event")
	}
	snapshot, err := e.snapshot()
	if err != nil {
		return pageResponse{}, err
	}
	catalog, err := snapshot.load(nil)
	if err != nil {
		return pageResponse{}, err
	}
	scenarios, err := e.scenarios()
	if err != nil {
		return pageResponse{}, err
	}
	ids := []string{q.Scenario}
	if q.Situation != "" {
		situations, err := e.situations()
		if err != nil {
			return pageResponse{}, err
		}
		s, ok := situations[q.Situation]
		if !ok {
			return pageResponse{}, fmt.Errorf("unknown situation %s", q.Situation)
		}
		if err := s.missingScenario(scenarios); err != nil {
			return pageResponse{}, err
		}
		ids = s.Scenarios
	}
	if q.Event != "" {
		r, event, err := eventParts(q.Event)
		if err != nil {
			return pageResponse{}, err
		}
		fields, err := catalog.Fields(r, event)
		if err != nil {
			return pageResponse{}, err
		}
		values := prompts.Values{}
		for _, field := range fields {
			values[field.Name] = ""
			if field.Kind == "flag" {
				values[field.Name] = "false"
			}
		}
		scenarios = maps.Clone(scenarios)
		scenarios[bareEvent] = scenario{Recipient: r, Event: event, Values: values}
		ids = []string{bareEvent}
	}
	scenarios = flipped(catalog, scenarios, q.Flips)
	response := pageResponse{Events: []pageEvent{}, Sources: map[string]pageSource{}}
	if err := e.freshness(snapshot); err != nil {
		response.Stale = err.Error()
	}
	for _, id := range ids {
		event, err := renderPageEvent(catalog, scenarios, id, map[string]bool{})
		if err != nil {
			return pageResponse{}, err
		}
		if id == bareEvent {
			event.Scenario = ""
		}
		response.Events = append(response.Events, event)
	}
	onPage, paths := map[string]bool{}, map[string]bool{}
	var collect func(pageEvent)
	collect = func(event pageEvent) {
		onPage[event.Event] = true
		for _, path := range nodeSources(event.Definition.Body) {
			paths[path] = true
		}
		for _, producer := range event.Producers {
			collect(producer)
		}
	}
	for _, event := range response.Events {
		collect(event)
	}
	for path := range paths {
		uses := slices.DeleteFunc(findUsages(catalog, path), func(u usage) bool { return onPage[u.Event] })
		response.Sources[path] = pageSource{snapshot.Sources[path], uses}
	}
	if q.Base != "" {
		base, err := e.selectBase(ctx, q.Base, "merge-base")
		if err != nil {
			return pageResponse{}, err
		}
		response.BaseCommit = base.Commit
		response.Base = map[string]string{}
		for path := range paths {
			if before, ok := base.Sources[path]; ok {
				response.Base[path] = before.Text
			}
		}
	}
	return response, nil
}
