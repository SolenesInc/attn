package activity

import (
	_ "embed"
	"os"
	"strings"
)

type Input struct {
	State       string
	StateReason string
	Window      string
	Previous    string
}

type Template struct {
	Name string
	Body string
}

// SystemMarker splits a template into its invariant half and this run's data. The first
// half REPLACES the CLI's own system prompt, worth ~22K tokens of billed prefix.
const SystemMarker = "{{USER}}"

type Rendered struct {
	System string
	User   string
}

func (r Rendered) Chars() int { return len(r.System) + len(r.User) }

func LoadTemplate(name, path string) (Template, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Template{}, err
	}
	return Template{Name: name, Body: string(body)}, nil
}

// Render substitutes {{STATE}}, {{STATE_REASON}}, {{PREVIOUS}} and {{WINDOW}} as literal tokens, not text/template actions, so prompts carry braces unescaped.
func (t Template) Render(in Input) Rendered {
	reason := strings.TrimSpace(in.StateReason)
	if reason == "" {
		reason = "unspecified"
	}
	previous := strings.TrimSpace(in.Previous)
	if previous == "" {
		previous = "(none — this is the first line for this session)"
	}
	window := strings.TrimSpace(in.Window)
	if window == "" {
		window = "(nothing new since the last line)"
	}
	replacer := strings.NewReplacer(
		"{{STATE}}", strings.TrimSpace(in.State),
		"{{STATE_REASON}}", reason,
		"{{PREVIOUS}}", previous,
		"{{WINDOW}}", window,
	)
	body := replacer.Replace(t.Body)
	system, user, split := strings.Cut(body, SystemMarker)
	if !split {
		return Rendered{User: strings.TrimSpace(body)}
	}
	return Rendered{System: strings.TrimSpace(system), User: strings.TrimSpace(user)}
}

//go:embed prompts/baseline.md
var baselineBody string

func Baseline() Template { return Template{Name: "baseline", Body: baselineBody} }
