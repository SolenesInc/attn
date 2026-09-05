package main

import (
	"context"
	_ "embed"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/prompts"
)

//go:embed authoring.md
var authoringWorkflow string

type contextSource struct {
	Current           *source `json:"current,omitempty"`
	Base              *source `json:"base,omitempty"`
	Diff              string  `json:"diff,omitempty"`
	Uses              []usage `json:"uses"`
	BaseUses          []usage `json:"base_uses,omitempty"`
	BaseSameAsCurrent bool    `json:"base_same_as_current,omitempty"`
}

type contextEvent struct {
	Event             string         `json:"event"`
	Current           *prompts.Event `json:"current,omitempty"`
	Base              *prompts.Event `json:"base,omitempty"`
	BaseSameAsCurrent bool           `json:"base_same_as_current,omitempty"`
}

type contextPrompt struct {
	Status   string   `json:"status"`
	Delivery string   `json:"delivery,omitempty"`
	Text     string   `json:"text,omitempty"`
	Sources  []string `json:"sources,omitempty"`
	Error    string   `json:"error,omitempty"`
}

type contextScenario struct {
	Label             string            `json:"label"`
	Scenario          string            `json:"scenario,omitempty"`
	Event             string            `json:"event"`
	Values            prompts.Values    `json:"values,omitempty"`
	Inputs            map[string]string `json:"inputs,omitempty"`
	Current           contextPrompt     `json:"current"`
	Base              *contextPrompt    `json:"base,omitempty"`
	Diff              string            `json:"diff,omitempty"`
	BaseSameAsCurrent bool              `json:"base_same_as_current,omitempty"`
}

type contextReport struct {
	Guide                 string                   `json:"guide"`
	Workflow              string                   `json:"workflow"`
	Scope                 []string                 `json:"scope"`
	CatalogRevision       string                   `json:"catalog_revision"`
	BaseCommit            string                   `json:"base_commit,omitempty"`
	Unavailable           string                   `json:"unavailable,omitempty"`
	Sources               map[string]contextSource `json:"sources"`
	Events                []contextEvent           `json:"events"`
	Scenarios             []contextScenario        `json:"scenarios"`
	UnrenderedEvents      []string                 `json:"unrendered_events"`
	UnrenderedSources     []string                 `json:"unrendered_sources"`
	UnrenderedBaseEvents  []string                 `json:"unrendered_base_events,omitempty"`
	UnrenderedBaseSources []string                 `json:"unrendered_base_sources,omitempty"`
	Limits                []string                 `json:"limits"`
}

func eventIndex(c *prompts.Catalog) map[string]prompts.Event {
	result := map[string]prompts.Event{}
	if c != nil {
		for _, r := range c.Recipients() {
			for _, event := range r.Events {
				result[r.ID+"/"+event.ID] = event
			}
		}
	}
	return result
}

func nodeSources(node prompts.Node) []string {
	paths := map[string]bool{}
	visitNode(node, func(n prompts.Node) {
		if n.Source != "" {
			paths[n.Source] = true
		}
	})
	return slices.Sorted(maps.Keys(paths))
}

func contextScope(targets []string, current, base *prompts.Catalog, sources map[string]contextSource) (map[string]bool, map[string]bool, error) {
	events, paths := map[string]bool{}, map[string]bool{}
	for _, target := range targets {
		path := strings.TrimPrefix(target, "internal/prompts/")
		_, found := sources[path]
		if found {
			paths[path] = true
		}
		for _, catalog := range []*prompts.Catalog{current, base} {
			if catalog == nil {
				continue
			}
			roots := []string{target}
			if event, ok := eventIndex(catalog)[target]; ok {
				roots = append(roots, nodeSources(event.Body)...)
			}
			for _, root := range roots {
				for _, use := range findUsages(catalog, root) {
					found = true
					events[use.Event] = true
				}
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("unknown context target %q; use list or uses to find an event, fragment or source", target)
		}
	}
	for changed := true; changed; {
		changed = false
		for _, catalog := range []*prompts.Catalog{current, base} {
			for key, event := range eventIndex(catalog) {
				if !events[key] {
					continue
				}
				for _, field := range prompts.NodeFields(event.Body) {
					if field.From != "" && !events[field.From] {
						events[field.From], changed = true, true
					}
				}
				for _, path := range nodeSources(event.Body) {
					paths[path] = true
				}
			}
		}
	}
	return events, paths, nil
}

func (e *editor) authoringContext(ctx context.Context, q operationRequest, snapshot catalogSnapshot, selected *focus, catalog *prompts.Catalog, scenarios map[string]scenario) (contextReport, error) {
	report := contextReport{
		Guide: "docs/prompt-authoring.md", Workflow: authoringWorkflow,
		CatalogRevision: revision(snapshot.Manifest), Sources: map[string]contextSource{},
		Events: []contextEvent{}, Scenarios: []contextScenario{}, UnrenderedEvents: []string{}, UnrenderedSources: []string{},
		Limits: []string{
			"Scope follows source uses and declared producer links. Include semantically related instructions with --include EVENT_OR_SOURCE.",
			"Scenario outputs are examples, not exhaustive condition coverage. Inspect each event definition and its inputs; unrendered lists identify gaps in the current outputs.",
			"available_skill and reference mean available to load, not delivered. Trace adapters and loading behavior; external skills, charters and harness instructions may supply more context.",
		},
	}
	if q.DraftID != "" && q.ReviewID != "" {
		return report, fmt.Errorf("choose --draft or --review")
	}
	var base *baseRevision
	var err error
	if q.Base != "" {
		mode := q.Mode
		if mode == "" {
			mode = "merge-base"
		}
		selection, selectErr := e.selectBase(ctx, q.Base, mode)
		if selectErr != nil {
			return report, selectErr
		}
		base = selection.baseRevision
	} else if selected != nil && selected.BaseCommit != "" {
		base, err = e.readBase(ctx, selected.BaseCommit)
		if err != nil {
			return report, err
		}
	}
	allSources := map[string]contextSource{}
	for path, source := range snapshot.Sources {
		allSources[path] = contextSource{Current: &source}
	}
	var baseCatalog *prompts.Catalog
	if base != nil {
		report.BaseCommit, report.Unavailable, baseCatalog = base.Commit, base.Unavailable, base.catalog
		for path, source := range base.Sources {
			entry := allSources[path]
			entry.Base = &source
			allSources[path] = entry
		}
	}
	targets := append([]string{}, q.Include...)
	for _, target := range []string{q.ID, q.Event, q.Path} {
		if target != "" {
			targets = append(targets, target)
		}
	}
	targets = append(targets, snapshot.EditedSources...)
	if selected != nil {
		targets = append(targets, selected.Event)
		if selected.Path != "" {
			targets = append(targets, selected.Path)
		}
	}
	if (q.DraftID != "" || q.ReviewID != "") && base != nil {
		for path, source := range allSources {
			if source.Current == nil || source.Base == nil || source.Current.Text != source.Base.Text {
				targets = append(targets, path)
			}
		}
	}
	if q.ReviewID != "" && snapshot.EditedSources == nil && base == nil {
		report.Limits = append(report.Limits, "This older review has no recorded changed-source scope or base. Include other affected sources explicitly with --include.")
	}
	if q.Scenario != "" {
		s, ok := scenarios[q.Scenario]
		if !ok {
			return report, fmt.Errorf("unknown scenario %s", q.Scenario)
		}
		targets = append(targets, s.Recipient+"/"+s.Event)
	}
	if len(targets) == 0 {
		return report, fmt.Errorf("context needs an event, source, --scenario, --draft or --review; read docs/prompt-authoring.md before editing")
	}
	slices.Sort(targets)
	report.Scope = slices.Compact(targets)
	scope, paths, err := contextScope(report.Scope, catalog, baseCatalog, allSources)
	if err != nil {
		return report, err
	}
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		entry := allSources[path]
		entry.Uses = findUsages(catalog, path)
		if baseCatalog != nil {
			entry.BaseUses = findUsages(baseCatalog, path)
		}
		if base != nil {
			before, after := "", ""
			if entry.Base != nil {
				before = entry.Base.Text
			}
			if entry.Current != nil {
				after = entry.Current.Text
			}
			entry.Diff, err = unifiedDiff(ctx, before, after)
			if err != nil {
				return report, err
			}
		}
		if entry.Current != nil && entry.Base != nil && *entry.Current == *entry.Base {
			entry.Base, entry.BaseSameAsCurrent = nil, true
		}
		report.Sources[path] = entry
	}
	currentEvents, baseEvents := eventIndex(catalog), eventIndex(baseCatalog)
	for _, key := range slices.Sorted(maps.Keys(scope)) {
		entry := contextEvent{Event: key}
		if event, ok := currentEvents[key]; ok {
			entry.Current = &event
		}
		if event, ok := baseEvents[key]; ok {
			entry.Base = &event
		}
		if entry.Current != nil && reflect.DeepEqual(entry.Current, entry.Base) {
			entry.Base, entry.BaseSameAsCurrent = nil, true
		}
		report.Events = append(report.Events, entry)
	}
	for _, id := range slices.Sorted(maps.Keys(scenarios)) {
		s := scenarios[id]
		if scope[s.Recipient+"/"+s.Event] {
			report.Scenarios = append(report.Scenarios, contextScenario{Label: s.Description, Scenario: id, Event: s.Recipient + "/" + s.Event, Values: s.Values, Inputs: s.Inputs})
		}
	}
	if selected != nil {
		report.Scenarios = append(report.Scenarios, contextScenario{Label: "Selected draft or review inputs", Event: selected.Event, Values: selected.Values})
	}
	if q.Values != nil || len(q.Inputs) > 0 {
		s := scenario{Values: prompts.Values{}, Inputs: map[string]string{}}
		key := q.Event
		if _, ok := currentEvents[q.ID]; ok && key == "" {
			key = q.ID
		}
		if selected != nil && key == "" {
			key, s.Values = selected.Event, maps.Clone(selected.Values)
		}
		if q.Scenario != "" {
			s = scenarios[q.Scenario]
			key = s.Recipient + "/" + s.Event
			s.Values, s.Inputs = maps.Clone(s.Values), maps.Clone(s.Inputs)
		}
		if key == "" {
			return report, fmt.Errorf("custom inputs need an event or --scenario")
		}
		if s.Values == nil {
			s.Values = prompts.Values{}
		}
		if s.Inputs == nil {
			s.Inputs = map[string]string{}
		}
		for name, value := range q.Values {
			s.Values[name] = value
			delete(s.Inputs, name)
		}
		for name, input := range q.Inputs {
			s.Inputs[name] = input
			delete(s.Values, name)
		}
		report.Scenarios = append(report.Scenarios, contextScenario{Label: "Custom inputs", Event: key, Values: s.Values, Inputs: s.Inputs})
	}
	for _, event := range report.Events {
		if slices.ContainsFunc(report.Scenarios, func(s contextScenario) bool { return s.Event == event.Event }) {
			continue
		}
		definition := event.Current
		if definition == nil {
			definition = event.Base
		}
		if definition != nil && len(prompts.NodeFields(definition.Body)) == 0 {
			report.Scenarios = append(report.Scenarios, contextScenario{Label: "No inputs required", Event: event.Event, Values: prompts.Values{}})
		}
	}
	renderedEvents, renderedSources := map[string]bool{}, map[string]bool{}
	baseRenderedEvents, baseRenderedSources := map[string]bool{}, map[string]bool{}
	for i := range report.Scenarios {
		sample := &report.Scenarios[i]
		sample.Current = renderContext(catalog, scenarios, *sample)
		if sample.Current.Status == "present" {
			renderedEvents[sample.Event] = true
			for _, path := range sample.Current.Sources {
				renderedSources[path] = true
			}
		}
		if baseCatalog != nil {
			before := renderContext(baseCatalog, scenarios, *sample)
			if before.Status == "present" {
				baseRenderedEvents[sample.Event] = true
				for _, path := range before.Sources {
					baseRenderedSources[path] = true
				}
			}
			sample.Base = &before
			if before.Error == "" && sample.Current.Error == "" {
				sample.Diff, err = unifiedDiff(ctx, before.Text, sample.Current.Text)
				if err != nil {
					return report, err
				}
			}
			if reflect.DeepEqual(before, sample.Current) {
				sample.Base, sample.BaseSameAsCurrent = nil, true
			}
		}
	}
	for _, event := range report.Events {
		if event.Current != nil && !renderedEvents[event.Event] {
			report.UnrenderedEvents = append(report.UnrenderedEvents, event.Event)
		}
		if _, exists := baseEvents[event.Event]; exists && !baseRenderedEvents[event.Event] {
			report.UnrenderedBaseEvents = append(report.UnrenderedBaseEvents, event.Event)
		}
	}
	for _, path := range slices.Sorted(maps.Keys(report.Sources)) {
		if report.Sources[path].Current != nil && !renderedSources[path] {
			report.UnrenderedSources = append(report.UnrenderedSources, path)
		}
		if baseCatalog != nil && allSources[path].Base != nil && !baseRenderedSources[path] {
			report.UnrenderedBaseSources = append(report.UnrenderedBaseSources, path)
		}
	}
	return report, nil
}

func renderContext(catalog *prompts.Catalog, scenarios map[string]scenario, sample contextScenario) contextPrompt {
	r, event, err := eventParts(sample.Event)
	if err != nil {
		return contextPrompt{Status: "invalid", Error: err.Error()}
	}
	if _, err := catalog.Fields(r, event); err != nil {
		return contextPrompt{Status: "absent"}
	}
	inputs := maps.Clone(scenarios)
	if inputs == nil {
		inputs = map[string]scenario{}
	}
	inputs["@context"] = scenario{Recipient: r, Event: event, Values: sample.Values, Inputs: sample.Inputs}
	values, err := scenarioValues(catalog, inputs, "@context", map[string]bool{})
	if err != nil {
		return contextPrompt{Status: "invalid", Error: err.Error()}
	}
	result, err := catalog.Render(r, event, values)
	if err != nil {
		return contextPrompt{Status: "invalid", Error: err.Error()}
	}
	sources := map[string]bool{}
	var visit func(prompts.Trace)
	visit = func(trace prompts.Trace) {
		if trace.Source != "" && trace.Selected {
			sources[trace.Source] = true
		}
		for _, child := range trace.Children {
			visit(child)
		}
	}
	visit(result.Trace)
	return contextPrompt{Status: "present", Delivery: result.Delivery, Text: result.Text, Sources: slices.Sorted(maps.Keys(sources))}
}
