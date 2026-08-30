package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/hooks"
)

type Driver interface {
	Name() string

	DisplayName() string

	DefaultExecutable() string

	ExecutableEnvVar() string

	ResolveExecutable(configured string) string

	BuildCommand(opts SpawnOpts) *exec.Cmd

	BuildEnv(opts SpawnOpts) []string

	Capabilities() Capabilities
}

type Capabilities struct {
	HasHooks bool

	HasTranscript bool

	HasTranscriptWatcher bool

	HasClassifier bool

	HarnessSignals HarnessSignalKind

	HasResume bool

	HasYolo bool

	HasInitialPrompt bool

	HasWorkspaceContext bool

	HasModelPin bool

	HasEffortPin bool
}

type HarnessSignalKind string

const (
	HarnessSignalsNone   HarnessSignalKind = ""
	HarnessSignalsClaude HarnessSignalKind = "claude"
	HarnessSignalsCodex  HarnessSignalKind = "codex"
)

var capabilityEnvNameSanitizer = regexp.MustCompile(`[^A-Za-z0-9]+`)

func EffectiveCapabilities(d Driver) Capabilities {
	if d == nil {
		return Capabilities{}
	}
	caps := d.Capabilities()
	prefix := "ATTN_AGENT_" + envAgentKey(d.Name()) + "_"

	if v, ok := boolEnv(prefix + "HOOKS"); ok {
		caps.HasHooks = v
	}
	if v, ok := boolEnv(prefix + "TRANSCRIPT"); ok {
		caps.HasTranscript = v
	}
	if v, ok := boolEnv(prefix + "TRANSCRIPT_WATCHER"); ok {
		caps.HasTranscriptWatcher = v
	}
	if v, ok := boolEnv(prefix + "CLASSIFIER"); ok {
		caps.HasClassifier = v
	}
	if v, ok := boolEnv(prefix + "HARNESS_SIGNALS"); ok && !v {
		caps.HarnessSignals = HarnessSignalsNone
	}
	if v, ok := boolEnv(prefix + "RESUME"); ok {
		caps.HasResume = v
	}
	if v, ok := boolEnv(prefix + "YOLO"); ok {
		caps.HasYolo = v
	}
	if v, ok := boolEnv(prefix + "INITIAL_PROMPT"); ok {
		caps.HasInitialPrompt = v
	}
	if v, ok := boolEnv(prefix + "WORKSPACE_CONTEXT"); ok {
		caps.HasWorkspaceContext = v
	}
	if v, ok := boolEnv(prefix + "MODEL_PIN"); ok {
		caps.HasModelPin = v
	}
	if v, ok := boolEnv(prefix + "EFFORT_PIN"); ok {
		caps.HasEffortPin = v
	}

	if !caps.HasTranscript {
		caps.HasTranscriptWatcher = false
	}
	return caps
}

func envAgentKey(name string) string {
	up := strings.ToUpper(strings.TrimSpace(name))
	up = capabilityEnvNameSanitizer.ReplaceAllString(up, "_")
	up = strings.Trim(up, "_")
	if up == "" {
		return "UNKNOWN"
	}
	return up
}

func boolEnv(key string) (bool, bool) {
	raw, ok := os.LookupEnv(key)
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

type SpawnOpts struct {
	SessionID       string
	CWD             string
	Label           string
	InitialPrompt   string
	Cols            uint16
	Rows            uint16
	ResumeSessionID string
	ResumePicker    bool
	YoloMode        bool

	AutoApprove bool

	Model string

	Effort string

	AutoCompactWindow int

	Executable string

	SocketPath string

	WrapperPath string

	SettingsPath string

	WorkspaceContextPath string

	InjectWorkflowGuidance bool

	NotebookRoot string

	ConfigOverrides []string

	TrustWorkingDirectory bool

	Garden bool

	CrewPriming string

	AwarenessDirs []string
}

func (o SpawnOpts) addDirArgs() []string {
	args := make([]string, 0, len(o.AwarenessDirs)*2)
	for _, dir := range o.AwarenessDirs {
		if dir = strings.TrimSpace(dir); dir != "" {
			args = append(args, "--add-dir", dir)
		}
	}
	return args
}

func (o SpawnOpts) launchGuidance() string {
	return hooks.Launch{
		NotebookRoot:         o.NotebookRoot,
		WorkspaceContextPath: o.WorkspaceContextPath,
		InjectWorkflow:       o.InjectWorkflowGuidance,
		Garden:               o.Garden,
		Crew:                 o.CrewPriming,
	}.Instructions()
}

type HookProvider interface {
	GenerateHooksConfig(opts SpawnOpts) string
}

type ConfigOverrideProvider interface {
	GenerateConfigOverrides(opts SpawnOpts) []string
}

type HeadlessTaskRequest struct {
	Executable       string
	Model            string
	ReasoningEffort  string
	Prompt           string
	WorkDir          string
	MCPServerName    string
	MCPServerCommand string
	MCPServerArgs    []string

	ToolName   string
	Schema     json.RawMessage
	ResultPath string

	Sandbox         string
	CWD             string
	ExtraMCPServers []MCPServerSpec

	AllowedTools []string

	// Empty AllowedTools falls back to the provider default set; only this runs with no
	// tools. Uncallable but not free: the definitions still ship in the billed prefix.
	DisableTools bool

	// Claude: IGNORED (dontAsk is not fs-sandboxed, writes anywhere already).
	ExtraWritableRoots []string

	// MaxTurns and MaxBudgetUSD are Claude-only. Codex and Claude both honor OutputSchema.
	MaxTurns     int
	MaxBudgetUSD string
	OutputSchema json.RawMessage

	// REPLACES the agent CLI's own system prompt. Measured on claude-haiku-4-5, tool-less,
	// with a --json-schema answer: the billed prefix drops from ~49.8K tokens to ~37.0K.
	SystemPrompt string
}

func (r HeadlessTaskRequest) usesNativeToolsPath() bool {
	return strings.TrimSpace(r.MCPServerName) == "" &&
		strings.TrimSpace(r.MCPServerCommand) == "" &&
		len(r.ExtraMCPServers) == 0 &&
		strings.TrimSpace(r.CWD) == "" &&
		strings.TrimSpace(r.Sandbox) == ""
}

type MCPServerSpec struct {
	Name         string
	Command      string
	Args         []string
	EnabledTools []string
}

type HeadlessTaskResult struct {
	Diagnostics      string
	FailureOutput    string
	Text             string
	StructuredOutput json.RawMessage
	TotalCostUSD     float64
	NumTurns         int
}

type HeadlessTaskProvider interface {
	RunHeadlessTask(ctx context.Context, request HeadlessTaskRequest) (HeadlessTaskResult, error)
}

type HeadlessTaskAvailabilityProvider interface {
	HeadlessTaskAvailability() (bool, string)
}

type ToolFreeOnlyHeadlessTaskProvider interface {
	HeadlessTasksAreToolFreeOnly() bool
}

func HeadlessTaskAvailability(driver Driver) (bool, string) {
	if driver == nil {
		return false, "agent is not installed"
	}
	if _, ok := driver.(HeadlessTaskProvider); !ok {
		return false, "agent does not support headless tasks"
	}
	if provider, ok := driver.(HeadlessTaskAvailabilityProvider); ok {
		return provider.HeadlessTaskAvailability()
	}
	return true, ""
}

func HeadlessTasksSupportTools(driver Driver) bool {
	if driver == nil {
		return false
	}
	if _, ok := driver.(HeadlessTaskProvider); !ok {
		return false
	}
	if provider, ok := driver.(ToolFreeOnlyHeadlessTaskProvider); ok {
		return !provider.HeadlessTasksAreToolFreeOnly()
	}
	return true
}

type TranscriptFinder interface {
	FindTranscript(sessionID, cwd string, startedAt time.Time) string

	FindTranscriptForResume(resumeID string) string

	BootstrapBytes() int64
}

type ClassifierProvider interface {
	Classify(text string, timeout time.Duration) (string, error)
}

type LaunchPreparer interface {
	PrepareLaunch(opts SpawnOpts) error
}

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Driver)
)

func Register(d Driver) {
	registryMu.Lock()
	defer registryMu.Unlock()
	name := d.Name()
	if _, exists := registry[name]; exists {
		panic("agent: driver already registered: " + name)
	}
	registry[name] = d
}

func Get(name string) Driver {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[name]
}

func List() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

func MustGet(name string) Driver {
	d := Get(name)
	if d == nil {
		panic("agent: unknown driver: " + name)
	}
	return d
}

func GetHookProvider(d Driver) (HookProvider, bool) {
	if d == nil || !EffectiveCapabilities(d).HasHooks {
		return nil, false
	}
	hp, ok := d.(HookProvider)
	return hp, ok
}

func GetConfigOverrideProvider(d Driver) (ConfigOverrideProvider, bool) {
	if d == nil || !EffectiveCapabilities(d).HasHooks {
		return nil, false
	}
	cp, ok := d.(ConfigOverrideProvider)
	return cp, ok
}

func GetTranscriptFinder(d Driver) (TranscriptFinder, bool) {
	if d == nil || !EffectiveCapabilities(d).HasTranscript {
		return nil, false
	}
	tf, ok := d.(TranscriptFinder)
	return tf, ok
}

func GetClassifier(d Driver) (ClassifierProvider, bool) {
	if d == nil || !EffectiveCapabilities(d).HasClassifier {
		return nil, false
	}
	cp, ok := d.(ClassifierProvider)
	return cp, ok
}

func GetTranscriptWatcherBehavior(d Driver) (TranscriptWatcherBehavior, bool) {
	if d == nil {
		return nil, false
	}
	caps := EffectiveCapabilities(d)
	if !caps.HasTranscript || !caps.HasTranscriptWatcher {
		return nil, false
	}
	if p, ok := d.(TranscriptWatcherBehaviorProvider); ok {
		behavior := p.NewTranscriptWatcherBehavior()
		if behavior != nil {
			behavior.Reset()
			return behavior, true
		}
	}
	behavior := newDefaultTranscriptWatcherBehavior()
	behavior.Reset()
	return behavior, true
}
