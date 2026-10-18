package activity

import "github.com/victorarias/attn/internal/prompts"

type Input struct {
	State       string
	StateReason string
	Window      string
	Previous    string
}

type Rendered struct {
	System string
	User   string
}

func Render(in Input) Rendered {
	values := prompts.Values{"state": in.State, "state_reason": in.StateReason, "previous": in.Previous, "window": in.Window}
	return Rendered{System: prompts.RenderText("activity", "system", nil), User: prompts.RenderText("activity", "user", values)}
}
