package harness

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

const AgentCredentialEnv = "ATTN_AGENT_CREDENTIAL"

var ApprovedAgentTools = []string{"attn_record", "skill", "ask"}

type DirectLaunchOpts struct {
	TerminalID        protocol.TerminalID
	CWD               string
	InitialPrompt     string
	ResumeSessionID   string
	ResumePicker      bool
	YoloMode          bool
	AutoApprove       bool
	Model             string
	Effort            string
	AutoCompactWindow int
	Executable        string
	SocketPath        string
	WrapperPath       string
	Guidance          prompts.Launch
	AwarenessDirs     []string
	AgentTools        *AgentTools
	SharedAgentTools  *AgentTools
}

func (o DirectLaunchOpts) EffectiveGuidance() prompts.Launch {
	g := o.Guidance
	g.AgentTools = o.AgentTools != nil || o.SharedAgentTools != nil
	return g
}

func ResolveExecutable(envVar, configured, fallback string) string {
	if v := strings.TrimSpace(configured); v != "" {
		return v
	}
	if envVar != "" {
		if v := strings.TrimSpace(os.Getenv(envVar)); v != "" {
			return v
		}
	}
	return fallback
}

var capabilityEnvNameSanitizer = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`[^A-Za-z0-9]+`)
})

func CapabilityEnv(id ID, suffix string) (bool, bool) {
	up := strings.ToUpper(strings.TrimSpace(id))
	up = capabilityEnvNameSanitizer().ReplaceAllString(up, "_")
	up = strings.Trim(up, "_")
	if up == "" {
		up = "UNKNOWN"
	}
	raw, ok := os.LookupEnv("ATTN_AGENT_" + up + "_" + suffix)
	if !ok {
		return false, false
	}
	value := strings.TrimSpace(strings.ToLower(raw))
	switch value {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	if parsed, err := strconv.ParseBool(value); err == nil {
		return parsed, true
	}
	return false, false
}

func ApplyCapabilityEnv(f Facts) Facts {
	if v, ok := CapabilityEnv(f.ID, "HOOKS"); ok {
		if v {
			f.Signals.TurnStarts = TurnStartsReported
			f.Signals.ReportsTools = true
		} else {
			f.Signals.TurnStarts = TurnStartsInferred
			f.Signals.ReportsTools = false
			f.Signals.ReportsBackgroundTasks = false
			f.Signals.ReportsSteps = false
		}
	}
	if v, ok := CapabilityEnv(f.ID, "TRANSCRIPT"); ok {
		f.Signals.StopsReadable = v
	}
	if v, ok := CapabilityEnv(f.ID, "CLASSIFIER"); ok {
		f.Signals.StopsClassified = v
	}
	if v, ok := CapabilityEnv(f.ID, "HARNESS_SIGNALS"); ok && !v {
		f.Signals.Titles = TitlesNone
	}
	if v, ok := CapabilityEnv(f.ID, "RESUME"); ok {
		f.Options.Resume = v
	}
	if v, ok := CapabilityEnv(f.ID, "YOLO"); ok {
		f.Options.Yolo = v
	}
	if v, ok := CapabilityEnv(f.ID, "INITIAL_PROMPT"); ok {
		f.Options.InitialPrompt = v
	}
	if v, ok := CapabilityEnv(f.ID, "LAUNCH_INSTRUCTIONS"); ok {
		f.Options.LaunchInstructions = v
	}
	if v, ok := CapabilityEnv(f.ID, "MODEL_PIN"); ok {
		f.Options.ModelPin = v
	}
	if v, ok := CapabilityEnv(f.ID, "EFFORT_PIN"); ok {
		f.Options.EffortPin = v
	}
	return f
}
