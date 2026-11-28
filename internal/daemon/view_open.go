package daemon

import (
	"path/filepath"
	"slices"

	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/viewlang"
)

func (d *Daemon) storedViewTileParams(profileID, view string) (string, error) {
	if !d.on(setting.ViewsEnabled) {
		return "", profiles.Errorf(profiles.CodeInvalid, "a tile shows the view %q only while the experimental setting views_enabled is on", view)
	}
	if !slices.Contains(profiles.BuiltInViews, view) {
		if _, kept, err := d.store.KeptView(profileID, view); err != nil || !kept {
			return "", profiles.Errorf(profiles.CodeInvalid, "tile_params name the view %q; attn's views are attn, sessions and pull-requests, and this profile keeps no view %q", view, view)
		}
	}
	return profiles.ViewParams(view), nil
}

// readView checks the view at path, whose text is content, and reads the
// values of the files it names from its folder and of its commands' last runs.
func (d *Daemon) readView(path, content string) *protocol.ViewDocument {
	checked := viewlang.Check(content)
	commands := viewCommands(checked.Parts)
	sources, values := d.viewSources(path, checked.Parts, commands)
	return &protocol.ViewDocument{Language: checked.Language, Parts: checked.Parts, Problems: checked.Problems, Sources: sources, Values: values, Commands: d.viewRunCommands(path, commands)}
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
