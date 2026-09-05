package activity

import (
	"io/fs"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/prompts"
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
	if t.Body == baselineBody {
		values := prompts.Values{"state": in.State, "state_reason": in.StateReason, "previous": in.Previous, "window": in.Window}
		return Rendered{System: prompts.RenderText("activity", "system", nil), User: prompts.RenderText("activity", "user", values)}
	}
	reason := prompts.RenderText("activity", "state-reason", prompts.Values{"state_reason": in.StateReason})
	previous := prompts.RenderText("activity", "previous", prompts.Values{"previous": in.Previous})
	window := prompts.RenderText("activity", "window", prompts.Values{"window": in.Window})
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

var baselineBody = func() string {
	data, err := fs.ReadFile(prompts.Files(), "content/activity/baseline.md")
	if err != nil {
		panic(err)
	}
	return string(data)
}()

func Baseline() Template { return Template{Name: "baseline", Body: baselineBody} }
