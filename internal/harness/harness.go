package harness

import (
	"context"
	"fmt"
	"time"
)

// ID names a harness: a built-in's name, or the agent a plugin registered.
type ID = string

// Harness is one adapter: everything attn knows about a harness and asks of it.
type Harness interface {
	Facts() Facts
	Health(ctx context.Context) Health
}

// Facts are what a harness is, constant for a built-in and fixed by the registration for a plugin.
type Facts struct {
	ID          ID
	DisplayName string
	Options     Options
	Executable  Executable
	SkillDirs   func(lookupEnv func(string) string, homeDir string) ([]string, error)
	CacheTTL    time.Duration
}

// Executable is how attn finds a harness's program when no setting names one: an env variable, then a default name. Empty for a harness that resolves its own.
type Executable struct {
	Default string
	Env     string
}

// Options are the launch options a harness takes.
type Options struct {
	Resume             bool
	Yolo               bool
	AutoApprove        bool
	InitialPrompt      bool
	LaunchInstructions bool
	ModelPin           bool
	EffortPin          bool
	ContextCap         bool
	Picker             bool
	Provider           bool
}

// Request is the launch options a caller asks of a harness.
type Request struct {
	InitialPrompt bool
	Model         bool
	Effort        bool
	Yolo          bool
	AutoApprove   bool
	ContextCap    bool
}

// Accept refuses the first option in r that the harness does not take.
func (f Facts) Accept(r Request) error {
	for _, option := range []struct {
		asked, taken bool
		refusal      string
	}{
		{r.InitialPrompt, f.Options.InitialPrompt, "does not support initial prompts"},
		{r.Model, f.Options.ModelPin, "does not support model pins"},
		{r.Effort, f.Options.EffortPin, "does not support effort pins"},
		{r.Yolo, f.Options.Yolo, "does not support yolo launches"},
		{r.AutoApprove, f.Options.AutoApprove, "does not support automation automatic approval"},
		{r.ContextCap, f.Options.ContextCap, "takes no context-window cap"},
	} {
		if option.asked && !option.taken {
			return fmt.Errorf("agent %q %s", f.ID, option.refusal)
		}
	}
	return nil
}

// Installation says whether the harness can run on this machine now.
type Installation uint8

const (
	// Unknown: attn cannot tell yet, as while the login environment is captured.
	Unknown Installation = iota
	Installed
	Missing
)

// Health is whether the harness can start a session now, and whether attn offers it for new ones.
type Health struct {
	Installation Installation
	Offered      bool
	Executable   string
	Reason       string
}
