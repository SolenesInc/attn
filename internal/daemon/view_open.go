package daemon

import (
	"encoding/json"
	"path/filepath"
	"slices"

	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/viewlang"
)

var builtInViews = []string{"attn", "sessions", "pull-requests"}

type namedView struct {
	View string `json:"view"`
}

func viewNamedByTileParams(params string) (string, bool) {
	var named namedView
	if json.Unmarshal([]byte(params), &named) != nil || named.View == "" {
		return "", false
	}
	return named.View, true
}

func (d *Daemon) storedViewTileParams(view string) (string, error) {
	if !d.on(setting.ViewsEnabled) {
		return "", profiles.Errorf(profiles.CodeInvalid, "a tile shows the view %q only while the experimental setting views_enabled is on", view)
	}
	if !slices.Contains(builtInViews, view) {
		return "", profiles.Errorf(profiles.CodeInvalid, "tile_params name the view %q; attn's views are attn, sessions and pull-requests", view)
	}
	encoded, _ := json.Marshal(namedView{View: view})
	return string(encoded), nil
}

// readView checks the view at path, whose text is content, and reads the
// values of the files it names from its folder.
func readView(path, content string) *protocol.ViewDocument {
	checked := viewlang.Check(content)
	sources, values := viewSources(filepath.Dir(path), checked.Parts)
	return &protocol.ViewDocument{Language: checked.Language, Parts: checked.Parts, Problems: checked.Problems, Sources: sources, Values: values}
}

// viewFiles is the files the view at path shows besides itself.
func viewFiles(path, content string) []string {
	return viewDataFiles(filepath.Dir(path), viewlang.Check(content).Parts)
}

// openProblems is what open answers about a view's problems.
func openProblems(view *protocol.ViewDocument) []protocol.ReviewProblem {
	problems := make([]protocol.ReviewProblem, 0, len(view.Problems))
	for _, problem := range view.Problems {
		problems = append(problems, protocol.ReviewProblem{Line: problem.Line, Message: problem.Text + " " + problem.Fix})
	}
	return problems
}
