package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/prompts"
)

type operationRequest struct {
	Op        string            `json:"op"`
	ID        string            `json:"id,omitempty"`
	Title     string            `json:"title,omitempty"`
	Event     string            `json:"event,omitempty"`
	Scenario  string            `json:"scenario,omitempty"`
	Path      string            `json:"path,omitempty"`
	Text      string            `json:"text,omitempty"`
	Expect    string            `json:"expect,omitempty"`
	Revision  int64             `json:"revision,omitempty"`
	Author    string            `json:"author,omitempty"`
	Values    prompts.Values    `json:"values,omitempty"`
	Inputs    map[string]string `json:"inputs,omitempty"`
	DraftID   string            `json:"draft,omitempty"`
	ReviewID  string            `json:"review,omitempty"`
	Base      string            `json:"base,omitempty"`
	Mode      string            `json:"mode,omitempty"`
	Target    string            `json:"target,omitempty"`
	Selection string            `json:"selection,omitempty"`
	Focus     *focus            `json:"focus,omitempty"`
	Include   []string          `json:"include,omitempty"`
}

func (e *editor) dataset(draftID, reviewID string) (catalogSnapshot, *focus, error) {
	if draftID == "" && reviewID == "" {
		var s catalogSnapshot
		_, err := e.withState(func(*os.Root) (any, error) { var err error; s, err = e.snapshot(); return nil, err })
		return s, nil, err
	}
	var snapshot catalogSnapshot
	var selected focus
	_, err := e.withState(func(root *os.Root) (any, error) {
		if reviewID != "" {
			r, err := readReview(root, reviewID)
			if err != nil {
				return nil, err
			}
			snapshot, selected = r.Snapshot, r.Focus
		} else {
			d, err := readDraft(root, draftID)
			if err != nil {
				return nil, err
			}
			snapshot, err = e.draftSnapshot(d)
			if err != nil {
				return nil, err
			}
			selected = d.Focus
		}
		return nil, nil
	})
	return snapshot, &selected, err
}

func eventParts(key string) (string, string, error) {
	r, event, ok := strings.Cut(key, "/")
	if !ok || r == "" || event == "" {
		return "", "", fmt.Errorf("event must be recipient/event")
	}
	return r, event, nil
}

func (e *editor) operation(ctx context.Context, q operationRequest) (any, error) {
	if q.Author == "" {
		q.Author = "maintainer"
	}
	switch q.Op {
	case "authoring":
		return map[string]any{"guide": "docs/prompt-authoring.md", "workflow": authoringWorkflow}, nil
	case "refresh":
		return e.withState(func(*os.Root) (any, error) { return e.refresh(ctx) })
	case "scenarios":
		return e.scenarios()
	case "scenario-save":
		return e.saveScenario(q)
	case "draft-create", "draft-list", "draft-get", "draft-put", "draft-reset", "draft-apply", "draft-sync", "draft-focus", "draft-archive", "draft-restore", "review-create", "review-list", "review-get", "review-comment":
		return e.withState(func(root *os.Root) (any, error) { return e.stateOperation(root, q) })
	}
	snapshot, selected, err := e.dataset(q.DraftID, q.ReviewID)
	if err != nil {
		return nil, err
	}
	if q.Op == "catalog" {
		v, err := snapshotView(snapshot)
		if err == nil && q.ReviewID == "" {
			if freshness := e.freshness(snapshot); freshness != nil {
				v.Validation = freshness.Error()
			}
		}
		return v, err
	}
	if q.ReviewID == "" {
		if err := e.freshness(snapshot); err != nil {
			return nil, err
		}
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
	scenarios, err := e.selectedScenarios(snapshot, q.ReviewID)
	if err != nil {
		return nil, err
	}
	if q.Op == "context" {
		return e.authoringContext(ctx, q, snapshot, selected, catalog, scenarios)
	}
	if q.Op == "check" || q.Op == "compare" {
		return e.checkScenarios(ctx, q, snapshot, catalog, scenarios)
	}
	if q.Op != "inspect" {
		return nil, fmt.Errorf("unknown operation %q", q.Op)
	}
	key, values := q.Event, q.Values
	if key == "" && selected != nil {
		key = selected.Event
		values = selected.Values
	}
	if q.Scenario != "" {
		s, ok := scenarios[q.Scenario]
		if !ok {
			return nil, fmt.Errorf("unknown scenario %s", q.Scenario)
		}
		key = s.Recipient + "/" + s.Event
		values, err = scenarioValues(catalog, scenarios, q.Scenario, map[string]bool{})
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
		values, err := scenarioValues(catalog, scenarios, name, map[string]bool{})
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
			baseValues, baseErr := scenarioValues(base.catalog, scenarios, name, map[string]bool{})
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
	result := map[string]any{"valid": valid, "scenarios": checks, "catalog_revision": revision(snapshot.Manifest)}
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
	snapshot, _, err := e.dataset(q.DraftID, "")
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
	values, err := scenarioValues(catalog, all, s.ID, map[string]bool{})
	if err != nil {
		return nil, err
	}
	if _, err := catalog.Render(r, event, values); err != nil {
		return nil, err
	}
	return e.withState(func(_ *os.Root) (any, error) {
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
	})
}

func (e *editor) stateOperation(root *os.Root, q operationRequest) (any, error) {
	switch q.Op {
	case "draft-list":
		return listState(root, "drafts")
	case "review-list":
		return listState(root, "reviews")
	case "review-get":
		return readReview(root, q.ID)
	case "draft-create":
		snapshot, err := e.snapshot()
		if err != nil {
			return nil, err
		}
		if _, err := snapshot.load(nil); err != nil {
			return nil, err
		}
		d := sharedDraft{Version: 1, ID: newID("d-"), Title: q.Title, Manifest: snapshot.Manifest, Files: map[string]draftFile{}, Focus: focus{Event: "session/launch", Values: prompts.Values{}}}
		if q.DraftID != "" {
			previous, err := readDraft(root, q.DraftID)
			if err != nil {
				return nil, err
			}
			d.Manifest = previous.Manifest
			d.Focus = previous.Focus
			for path, file := range previous.Files {
				d.Files[path] = file
			}
		}
		if q.ReviewID != "" {
			r, err := readReview(root, q.ReviewID)
			if err != nil {
				return nil, err
			}
			if !sameDefinitions(snapshot.Manifest, r.Snapshot.Manifest) {
				return nil, conflict("review uses different composition definitions", nil)
			}
			for name, file := range r.Snapshot.Sources {
				if current, ok := snapshot.Sources[name]; ok && current.Text != file.Text {
					d.Files[name] = draftFile{file.Text, current.Revision, file.Revision, q.Author}
				}
			}
			d.Focus = r.Focus
		}
		if q.Focus != nil {
			d.Focus = *q.Focus
		}
		if err := saveDraft(root, &d, q.Author); err != nil {
			return nil, err
		}
		return d, nil
	case "review-comment":
		r, err := readReview(root, q.ID)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(q.Text) == "" {
			return nil, fmt.Errorf("feedback needs a message")
		}
		comment := feedback{ID: newID("c-"), Author: q.Author, Message: q.Text, Target: q.Target, Selection: q.Selection, Path: q.Path, CreatedAt: now()}
		if q.Target == "source" {
			file, ok := r.Snapshot.Sources[q.Path]
			if !ok {
				return nil, fmt.Errorf("source not in review")
			}
			if q.Selection != "" && !strings.Contains(file.Text, q.Selection) {
				return nil, conflict("selection does not match the review source", nil)
			}
			comment.SourceRevision = file.Revision
		} else if q.Target == "prompt" {
			catalog, err := r.Snapshot.load(nil)
			if err != nil {
				return nil, err
			}
			recipient, event, err := eventParts(r.Focus.Event)
			if err != nil {
				return nil, err
			}
			result, err := catalog.Render(recipient, event, r.Focus.Values)
			if err != nil {
				return nil, err
			}
			if q.Selection != "" && !strings.Contains(result.Text, q.Selection) {
				return nil, conflict("selection does not match the reviewed prompt", nil)
			}
		} else if q.Target != "" {
			return nil, fmt.Errorf("feedback target must be source or prompt")
		}
		r.Feedback = append(r.Feedback, comment)
		if err := writeJSON(root, "reviews/"+r.ID+".json", r); err != nil {
			return nil, err
		}
		d, err := readDraft(root, r.DraftID)
		if err != nil {
			return nil, err
		}
		d.LatestReview = r.ID
		if err := saveDraft(root, &d, q.Author); err != nil {
			return nil, err
		}
		return r, nil
	}
	id := q.ID
	if q.DraftID != "" {
		id = q.DraftID
	}
	d, err := readDraft(root, id)
	if err != nil {
		return nil, err
	}
	if q.Op == "draft-get" {
		return d, nil
	}
	if d.Archived && q.Op != "draft-restore" {
		return nil, fmt.Errorf("draft is archived; restore it before editing")
	}
	if q.Op == "draft-apply" {
		return e.applyDraft(root, d, q.Revision, q.Author)
	}
	if q.Op == "review-create" {
		if q.Revision != d.Revision {
			return nil, conflict("draft changed; inspect it before creating a review", d)
		}
		snapshot, err := e.draftSnapshot(d)
		if err != nil {
			return nil, err
		}
		if err := e.freshness(snapshot); err != nil {
			return nil, err
		}
		f := d.Focus
		if q.Focus != nil {
			f = *q.Focus
		}
		catalog, err := snapshot.load(nil)
		if err != nil {
			return nil, err
		}
		recipient, event, err := eventParts(f.Event)
		if err != nil {
			return nil, err
		}
		snapshot.Scenarios, err = e.scenarios()
		if err != nil {
			return nil, err
		}
		if f.Scenario != "" {
			scenario, ok := snapshot.Scenarios[f.Scenario]
			if !ok || scenario.Recipient+"/"+scenario.Event != f.Event {
				return nil, fmt.Errorf("scenario does not match review event")
			}
			f.Values, err = scenarioValues(catalog, snapshot.Scenarios, f.Scenario, map[string]bool{})
			if err != nil {
				return nil, err
			}
		}
		if _, err := catalog.Render(recipient, event, f.Values); err != nil {
			return nil, err
		}
		r := review{Version: 1, ID: newID("r-"), Title: q.Title, DraftID: d.ID, DraftRevision: d.Revision, Author: q.Author, CreatedAt: now(), Focus: f, Snapshot: snapshot, Feedback: []feedback{}}
		if r.Title == "" {
			r.Title = d.Title
		}
		if err := writeJSON(root, "reviews/"+r.ID+".json", r); err != nil {
			return nil, err
		}
		d.LatestReview = r.ID
		if err := saveDraft(root, &d, q.Author); err != nil {
			return nil, err
		}
		return r, nil
	}
	switch q.Op {
	case "draft-put", "draft-reset":
		if !utf8.ValidString(q.Text) {
			return nil, fmt.Errorf("source text must be UTF-8")
		}
		snapshot, err := e.snapshot()
		if err != nil {
			return nil, err
		}
		file, ok := snapshot.Sources[q.Path]
		if edited, exists := d.Files[q.Path]; exists {
			file = source{edited.Text, edited.Revision}
			ok = ok || q.Op == "draft-reset"
		}
		if !ok {
			return nil, fmt.Errorf("unregistered source: %s", q.Path)
		}
		if file.Revision != q.Expect {
			return nil, conflict("source changed in the shared draft; inspect it before retrying", file)
		}
		if q.Op == "draft-reset" || q.Text == snapshot.Sources[q.Path].Text {
			delete(d.Files, q.Path)
		} else {
			base := snapshot.Sources[q.Path].Revision
			if previous, ok := d.Files[q.Path]; ok {
				base = previous.BaseRevision
			}
			d.Files[q.Path] = draftFile{q.Text, base, revision([]byte(q.Text)), q.Author}
		}
	case "draft-sync":
		if q.Revision != d.Revision {
			return nil, conflict("draft changed; reload before syncing", d)
		}
		snapshot, err := e.snapshot()
		if err != nil {
			return nil, err
		}
		for name, file := range d.Files {
			current, ok := snapshot.Sources[name]
			if !ok || current.Revision != file.BaseRevision {
				return nil, conflict("edited source also changed on disk; resolve it before syncing", name)
			}
		}
		if _, err := snapshot.load(draftTexts(d)); err != nil {
			return nil, err
		}
		d.Manifest = snapshot.Manifest
	case "draft-focus":
		if q.Focus == nil {
			return nil, fmt.Errorf("focus required")
		}
		d.Focus = *q.Focus
	case "draft-archive", "draft-restore":
		if q.Revision != d.Revision {
			return nil, conflict("draft changed; reload before changing its state", d)
		}
		d.Archived = q.Op == "draft-archive"
	default:
		return nil, fmt.Errorf("unknown operation %s", q.Op)
	}
	if err := saveDraft(root, &d, q.Author); err != nil {
		return nil, err
	}
	return d, nil
}
