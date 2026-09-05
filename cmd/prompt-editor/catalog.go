package main

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/prompts"
)

type catalogSnapshot struct {
	Manifest      json.RawMessage     `json:"manifest"`
	Sources       map[string]source   `json:"sources"`
	Scenarios     map[string]scenario `json:"scenarios,omitempty"`
	EditedSources []string            `json:"edited_sources"`
}

func visitNode(n prompts.Node, visit func(prompts.Node)) {
	visit(n)
	for _, b := range n.Bindings {
		visitNode(b.Node, visit)
	}
	for _, child := range n.Children {
		visitNode(child, visit)
	}
}

func snapshotView(snapshot catalogSnapshot) (catalogView, error) {
	manifest, err := prompts.ParseManifest(snapshot.Manifest)
	if err != nil {
		return catalogView{}, err
	}
	v := catalogView{Recipients: manifest.Recipients, Sources: snapshot.Sources, Fields: map[string][]prompts.Field{}, Revision: revision(snapshot.Manifest)}
	for _, r := range v.Recipients {
		for _, event := range r.Events {
			v.Fields[r.ID+"/"+event.ID] = prompts.NodeFields(event.Body)
		}
	}
	if _, err := snapshot.load(nil); err != nil {
		v.Validation = err.Error()
	}
	return v, nil
}

func (s catalogSnapshot) load(drafts map[string]string) (*prompts.Catalog, error) {
	texts := map[string]string{}
	for name, source := range s.Sources {
		if !utf8.ValidString(source.Text) {
			return nil, fmt.Errorf("%s must be UTF-8", name)
		}
		texts[name] = source.Text
	}
	for name, text := range drafts {
		if !utf8.ValidString(text) {
			return nil, fmt.Errorf("%s must be UTF-8", name)
		}
		if _, ok := texts[name]; !ok {
			return nil, fmt.Errorf("unregistered source: %s", name)
		}
		texts[name] = text
	}
	return prompts.LoadManifest(s.Manifest, overlayFS{FS: os.DirFS("/nonexistent-prompt-sources"), drafts: texts})
}

func (e *editor) snapshot() (catalogSnapshot, error) {
	data, err := e.root.ReadFile(prompts.ManifestPath)
	if err != nil {
		return catalogSnapshot{}, fmt.Errorf("read checkout catalog (run prompt-editor refresh): %w", err)
	}
	manifest, err := prompts.ParseManifest(data)
	if err != nil {
		return catalogSnapshot{}, err
	}
	snapshot := catalogSnapshot{Manifest: data, Sources: map[string]source{}}
	for _, r := range manifest.Recipients {
		for _, event := range r.Events {
			visitNode(event.Body, func(n prompts.Node) {
				if err != nil || n.Source == "" {
					return
				}
				if !fs.ValidPath(n.Source) || !strings.HasPrefix(n.Source, "content/") || !strings.HasSuffix(n.Source, ".md") {
					err = fmt.Errorf("unregistered source path: %s", n.Source)
					return
				}
				if _, exists := snapshot.Sources[n.Source]; exists {
					return
				}
				var text []byte
				text, err = e.root.ReadFile(n.Source)
				if err == nil {
					snapshot.Sources[n.Source] = source{string(text), revision(text)}
				}
			})
		}
	}
	return snapshot, err
}

func (e *editor) freshness(snapshot catalogSnapshot) error {
	manifest, err := prompts.ParseManifest(snapshot.Manifest)
	if err != nil {
		return err
	}
	if manifest.DefinitionsHash == "" {
		return nil
	}
	hash, err := prompts.DefinitionsHash(e.root.FS())
	if err != nil {
		return err
	}
	if hash != manifest.DefinitionsHash {
		return fmt.Errorf("composition declarations changed in Go; run prompt-editor refresh before inspecting or applying this checkout")
	}
	return nil
}

func (e *editor) refresh(ctx context.Context) (any, error) {
	cmd := exec.CommandContext(ctx, "go", "run", "./cmd/promptgen", "--repo", e.repo)
	cmd.Dir = e.repo
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOOS=") && !strings.HasPrefix(entry, "GOARCH=") && !strings.HasPrefix(entry, "CGO_ENABLED=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("catalog generation failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	snapshot, err := e.snapshot()
	if err != nil {
		return nil, err
	}
	return snapshotView(snapshot)
}

type scenario struct {
	Version     int               `json:"version"`
	ID          string            `json:"id"`
	Description string            `json:"description"`
	Recipient   string            `json:"recipient"`
	Event       string            `json:"event"`
	Values      prompts.Values    `json:"values"`
	Inputs      map[string]string `json:"inputs,omitempty"`
	Revision    string            `json:"revision,omitempty"`
}

func (e *editor) scenarios() (map[string]scenario, error) {
	names, err := fs.Glob(e.root.FS(), "scenarios/*.json")
	if err != nil {
		return nil, err
	}
	result := map[string]scenario{}
	for _, name := range names {
		data, err := e.root.ReadFile(name)
		if err != nil {
			return nil, err
		}
		var s scenario
		if err := decodeJSON(data, &s); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if s.Version != 1 || !validName.MatchString(s.ID) || name != "scenarios/"+s.ID+".json" {
			return nil, fmt.Errorf("%s: scenario version must be 1 and id must match filename", name)
		}
		s.Revision = revision(data)
		result[s.ID] = s
	}
	return result, nil
}

func scenarioValues(c *prompts.Catalog, scenarios map[string]scenario, id string, visiting map[string]bool) (prompts.Values, error) {
	s, ok := scenarios[id]
	if !ok {
		return nil, fmt.Errorf("unknown scenario %q", id)
	}
	if visiting[id] {
		return nil, fmt.Errorf("scenario input cycle at %s", id)
	}
	visiting[id] = true
	defer delete(visiting, id)
	fields, err := c.Fields(s.Recipient, s.Event)
	if err != nil {
		return nil, err
	}
	values := prompts.Values{}
	for name, value := range s.Values {
		values[name] = value
	}
	for name, producer := range s.Inputs {
		if _, exists := values[name]; exists {
			return nil, fmt.Errorf("scenario %s supplies %s twice", id, name)
		}
		from := ""
		for _, field := range fields {
			if field.Name == name {
				from = field.From
			}
		}
		p, ok := scenarios[producer]
		if !ok || from == "" || from != p.Recipient+"/"+p.Event {
			return nil, fmt.Errorf("scenario %s: %s must reference its declared producer %s", id, name, from)
		}
		inputs, err := scenarioValues(c, scenarios, producer, visiting)
		if err != nil {
			return nil, err
		}
		rendered, err := c.Render(p.Recipient, p.Event, inputs)
		if err != nil {
			return nil, err
		}
		values[name] = rendered.Text
	}
	return values, nil
}

type usage struct {
	Event string `json:"event"`
	Via   string `json:"via"`
}

func findUsages(c *prompts.Catalog, target string) []usage {
	found := map[string]string{}
	for _, r := range c.Recipients() {
		for _, event := range r.Events {
			key := r.ID + "/" + event.ID
			if key == target {
				found[key] = "event"
			}
			visitNode(event.Body, func(n prompts.Node) {
				if n.ID == target || n.Source == strings.TrimPrefix(target, "internal/prompts/") {
					found[key] = "source"
				}
			})
		}
	}
	for changed := true; changed; {
		changed = false
		for _, r := range c.Recipients() {
			for _, event := range r.Events {
				key := r.ID + "/" + event.ID
				if _, exists := found[key]; exists {
					continue
				}
				for _, field := range prompts.NodeFields(event.Body) {
					if _, ok := found[field.From]; ok {
						found[key] = field.From
						changed = true
						break
					}
				}
			}
		}
	}
	result := []usage{}
	for key, via := range found {
		result = append(result, usage{key, via})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Event < result[j].Event })
	return result
}

type declarationLocation struct {
	Path string `json:"path"`
	Line int    `json:"line"`
}

func (e *editor) declarations(event prompts.Event) []declarationLocation {
	needles := map[string]bool{}
	visitNode(event.Body, func(n prompts.Node) {
		if n.Source != "" {
			needles[n.Source] = true
			needles[n.ID] = true
		}
	})
	names, _ := fs.Glob(e.root.FS(), "*.go")
	locations := []declarationLocation{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := e.root.ReadFile(name)
		if err != nil {
			continue
		}
		positions := token.NewFileSet()
		file, err := parser.ParseFile(positions, name, data, 0)
		if err != nil {
			continue
		}
		seen := map[int]bool{}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil || !needles[value] {
				return true
			}
			line := positions.Position(literal.Pos()).Line
			if !seen[line] {
				locations = append(locations, declarationLocation{"internal/prompts/" + name, line})
				seen[line] = true
			}
			return true
		})
	}
	return locations
}

func sameDefinitions(a, b json.RawMessage) bool {
	left, err := prompts.ParseManifest(a)
	if err != nil {
		return false
	}
	right, err := prompts.ParseManifest(b)
	return err == nil && reflect.DeepEqual(left.Recipients, right.Recipients)
}

func openEditor(repo string) (*editor, error) {
	absolute, err := filepath.Abs(repo)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Join(absolute, "internal/prompts"))
	if err != nil {
		return nil, err
	}
	e := newEditor(root, os.DirFS("."), "")
	e.repo = absolute
	return e, nil
}

func (e *editor) selectedScenarios(snapshot catalogSnapshot, reviewID string) (map[string]scenario, error) {
	if reviewID != "" {
		return snapshot.Scenarios, nil
	}
	return e.scenarios()
}
func renderScenario(c *prompts.Catalog, request editRequest, scenarios map[string]scenario) (previewSide, prompts.Values) {
	values := request.Values
	if request.Scenario != "" {
		s, ok := scenarios[request.Scenario]
		if !ok || s.Recipient+"/"+s.Event != request.Recipient+"/"+request.Event {
			return previewSide{Status: "invalid", Error: "scenario does not match this event"}, nil
		}
		if _, err := c.Fields(request.Recipient, request.Event); err != nil {
			return previewSide{Status: "absent"}, nil
		}
		var err error
		values, err = scenarioValues(c, scenarios, request.Scenario, map[string]bool{})
		if err != nil {
			return previewSide{Status: "invalid", Error: err.Error()}, nil
		}
	}
	return renderSide(c, request.Recipient, request.Event, values), values
}
