// Package guidance is the typed catalogue behind the guidance-map spike.
//
// It deliberately does not feed production yet. The spike keeps the current
// injection paths untouched while exercising the proposed data shape over a
// structural shadow of their current copy and runtime conditions.
package guidance

import (
	"embed"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

type Agent string

const (
	AgentClaude Agent = "claude"
	AgentCodex  Agent = "codex"
	AgentPlugin Agent = "plugin"
)

type Role string

const (
	RolePlain    Role = "plain"
	RoleDelegate Role = "delegate"
	RoleCrew     Role = "crew"
	RoleChief    Role = "chief"
)

type GardenState string

const (
	GardenNoPlot    GardenState = "no_plot"
	GardenAtSeed    GardenState = "at_seed"
	GardenAtPlot    GardenState = "at_plot"
	GardenEmptyPlot GardenState = "empty_plot"
)

type Moment string

const (
	MomentLaunch       Moment = "launch system prompt"
	MomentSessionStart Moment = "SessionStart hook"
	MomentFirstPrompt  Moment = "first prompt"
	MomentNudge        Moment = "composer nudge"
	MomentHandoff      Moment = "handoff turnover"
	MomentCLI          Moment = "CLI / skill read"
)

type Delivery string

const (
	DeliveryClaudeSystem Delivery = "Claude --append-system-prompt"
	DeliveryCodexSystem  Delivery = "Codex developer_instructions"
	DeliveryPluginSystem Delivery = "plugin launch_instructions"
	DeliveryHookContext  Delivery = "hook additionalContext"
	DeliveryInitial      Delivery = "agent initial_prompt"
	DeliveryComposer     Delivery = "PTY composer input"
	DeliveryPluginMsg    Delivery = "plugin driver.deliver_message"
	DeliveryDoorbell     Delivery = "durable agent-message doorbell"
	DeliveryCLIStdout    Delivery = "CLI stdout/stderr"
	DeliverySkillRead    Delivery = "attn skill file read"
)

type Scenario struct {
	Home              bool        `json:"home"`
	Agent             Agent       `json:"agent"`
	Role              Role        `json:"role"`
	Garden            GardenState `json:"garden"`
	HasContext        bool        `json:"has_context"`
	WorkflowEnabled   bool        `json:"workflow_enabled"`
	LaunchHasGuidance bool        `json:"launch_has_guidance"`
	PluginLaunch      bool        `json:"plugin_launch_instructions"`
	PluginInitial     bool        `json:"plugin_initial_prompt"`
	PluginMessages    bool        `json:"plugin_message_delivery"`
}

func DefaultScenario() Scenario {
	return Scenario{
		Home: true, Agent: AgentCodex, Role: RoleDelegate, Garden: GardenAtPlot,
		HasContext: true, LaunchHasGuidance: true,
		PluginLaunch: true, PluginInitial: true, PluginMessages: true,
	}
}

func (s Scenario) normalized() Scenario {
	if s.Agent != AgentClaude && s.Agent != AgentCodex && s.Agent != AgentPlugin {
		s.Agent = AgentCodex
	}
	if s.Role != RolePlain && s.Role != RoleDelegate && s.Role != RoleCrew && s.Role != RoleChief {
		s.Role = RolePlain
	}
	if s.Garden != GardenNoPlot && s.Garden != GardenAtSeed && s.Garden != GardenAtPlot && s.Garden != GardenEmptyPlot {
		s.Garden = GardenNoPlot
	}
	return s
}

type predicate struct {
	label  string
	source string
	match  func(Scenario) bool
}

func always() predicate {
	return predicate{"Every session", "unconditional", func(Scenario) bool { return true }}
}

func home() predicate {
	return predicate{"Home daemon", "enrollment.Status.RequireHome", func(s Scenario) bool { return s.Home }}
}

func outpost() predicate {
	return predicate{"Outpost daemon", "!enrollment.Status.IsHome", func(s Scenario) bool { return !s.Home }}
}

func roleIs(role Role) predicate {
	return predicate{"Role is " + string(role), "Scenario.Role", func(s Scenario) bool { return s.Role == role }}
}

func roleIsNot(role Role) predicate {
	return predicate{"Role is not " + string(role), "Scenario.Role", func(s Scenario) bool { return s.Role != role }}
}

func gardenIs(state GardenState) predicate {
	return predicate{"Garden state is " + strings.ReplaceAll(string(state), "_", " "), "SeedReadyResult.Crown + Seeds", func(s Scenario) bool { return s.Garden == state }}
}

func contextPresent() predicate {
	return predicate{"Workspace context checkout exists", "WorkspaceContextResult.Path", func(s Scenario) bool { return s.HasContext }}
}

func workflowEnabled() predicate {
	return predicate{"Workflows setting enabled", "SettingWorkflowsEnabled", func(s Scenario) bool { return s.WorkflowEnabled }}
}

func launchGuidanceMissing() predicate {
	return predicate{"Launch-time guidance marker is absent", "ATTN_WORKSPACE_CONTEXT_GUIDANCE / ATTN_CHIEF_GUIDANCE", func(s Scenario) bool { return !s.LaunchHasGuidance }}
}

func builtInAgent() predicate {
	return predicate{"Claude or Codex hook runtime", "Scenario.Agent", func(s Scenario) bool { return s.Agent == AgentClaude || s.Agent == AgentCodex }}
}

func launchInstructionsCapable() predicate {
	return predicate{
		"Built-in agent or plugin with launch_instructions",
		"agent.Driver capabilities[launch_instructions]",
		func(s Scenario) bool { return s.Agent != AgentPlugin || s.PluginLaunch },
	}
}

func initialPromptCapable() predicate {
	return predicate{
		"Built-in agent or plugin with initial_prompt",
		"agent.Driver capabilities[initial_prompt]",
		func(s Scenario) bool { return s.Agent != AgentPlugin || s.PluginInitial },
	}
}

type ValueType string

const (
	ValueText     ValueType = "text"
	ValueMarkdown ValueType = "markdown"
	ValuePath     ValueType = "path"
	ValueID       ValueType = "id"
	ValueCount    ValueType = "count"
	ValueDuration ValueType = "duration"
	ValueList     ValueType = "list"
)

type SourceKind string

const (
	SourceScenario   SourceKind = "scenario"
	SourceProtocol   SourceKind = "protocol"
	SourceStore      SourceKind = "store"
	SourceFilesystem SourceKind = "filesystem"
	SourceRuntime    SourceKind = "runtime"
	SourceUser       SourceKind = "user"
)

// Field is deliberately generic: the example and the named source carry the
// same Go type. A count cannot be populated with a path without a compile error.
type Field[T any] struct {
	Name    string
	Kind    ValueType
	Source  Source[T]
	Example T
}

type Source[T any] struct {
	Kind SourceKind
	Path string
}

type field interface {
	definition() PlaceholderView
}

func (f Field[T]) definition() PlaceholderView {
	return PlaceholderView{
		Name: f.Name, Type: f.Kind, SourceKind: f.Source.Kind,
		Source: f.Source.Path, Example: fmt.Sprint(f.Example),
	}
}

type unit struct {
	id         string
	name       string
	summary    string
	moment     Moment
	trigger    string
	conditions []predicate
	delivery   func(Scenario) Delivery
	deliveries []DeliveryChoice
	copyPath   string
	sourcePath string
	fields     []field
	load       func() (string, error)
}

type composition struct {
	id         string
	name       string
	moment     Moment
	trigger    string
	conditions []predicate
	delivery   func(Scenario) Delivery
	blocks     []string
}

type Snapshot struct {
	Scenario     Scenario          `json:"scenario"`
	Axes         AxesView          `json:"axes"`
	Units        []UnitView        `json:"units"`
	Compositions []CompositionView `json:"compositions"`
	Moments      []Moment          `json:"moments"`
	Lifecycle    []LifecycleLane   `json:"lifecycle"`
}

type AxesView struct {
	Agents       []Agent       `json:"agents"`
	Roles        []Role        `json:"roles"`
	GardenStates []GardenState `json:"garden_states"`
}

type UnitView struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Summary         string            `json:"summary"`
	Moment          Moment            `json:"moment"`
	Trigger         string            `json:"trigger"`
	Eligible        bool              `json:"eligible"`
	Delivery        Delivery          `json:"delivery"`
	DeliveryChoices []DeliveryChoice  `json:"delivery_choices,omitempty"`
	Conditions      []ConditionView   `json:"conditions"`
	Copy            string            `json:"copy"`
	Preview         string            `json:"preview"`
	CopyPath        string            `json:"copy_path"`
	SourcePath      string            `json:"source_path"`
	Placeholders    []PlaceholderView `json:"placeholders"`
}

type DeliveryChoice struct {
	Agent    Agent    `json:"agent"`
	Delivery Delivery `json:"delivery"`
}

type ConditionView struct {
	Label   string `json:"label"`
	Source  string `json:"source"`
	Matches bool   `json:"matches"`
}

type PlaceholderView struct {
	Name       string     `json:"name"`
	Type       ValueType  `json:"type"`
	SourceKind SourceKind `json:"source_kind"`
	Source     string     `json:"source"`
	Example    string     `json:"example"`
}

type CompositionView struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Moment     Moment          `json:"moment"`
	Trigger    string          `json:"trigger"`
	Eligible   bool            `json:"eligible"`
	Delivery   Delivery        `json:"delivery"`
	Conditions []ConditionView `json:"conditions"`
	Blocks     []BlockView     `json:"blocks"`
}

type BlockView struct {
	Order    int    `json:"order"`
	UnitID   string `json:"unit_id"`
	Name     string `json:"name"`
	Eligible bool   `json:"eligible"`
}

type LifecycleLane struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Steps []LifecycleStep `json:"steps"`
}

type LifecycleStep struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Moment  Moment   `json:"moment"`
	UnitIDs []string `json:"unit_ids"`
	Next    []string `json:"next,omitempty"`
}

//go:embed text/*.md
var textFiles embed.FS

func embedded(path string) func() (string, error) {
	return func() (string, error) {
		data, err := textFiles.ReadFile("text/" + path)
		return strings.TrimSpace(string(data)), err
	}
}

func skill(relative string) func() (string, error) {
	return func() (string, error) {
		data, err := agentdriver.SkillFile(relative)
		return strings.TrimSpace(string(data)), err
	}
}

func launchDelivery(s Scenario) Delivery {
	switch s.Agent {
	case AgentClaude:
		return DeliveryClaudeSystem
	case AgentPlugin:
		return DeliveryPluginSystem
	default:
		return DeliveryCodexSystem
	}
}

func fixedDelivery(d Delivery) func(Scenario) Delivery {
	return func(Scenario) Delivery { return d }
}

func nudgeDelivery(s Scenario) Delivery {
	if s.Agent == AgentPlugin && s.PluginMessages {
		return DeliveryPluginMsg
	}
	return DeliveryComposer
}

var launchChoices = []DeliveryChoice{
	{AgentClaude, DeliveryClaudeSystem},
	{AgentCodex, DeliveryCodexSystem},
	{AgentPlugin, DeliveryPluginSystem},
}

func textField(name, path, example string) field {
	return Field[string]{name, ValueText, Source[string]{SourceProtocol, path}, example}
}

func userMarkdownField(name, path, example string) field {
	return Field[string]{name, ValueMarkdown, Source[string]{SourceUser, path}, example}
}

func pathField(name, path, example string) field {
	return Field[string]{name, ValuePath, Source[string]{SourceFilesystem, path}, example}
}

func idField(name, path, example string) field {
	return Field[string]{name, ValueID, Source[string]{SourceProtocol, path}, example}
}

func countField(name, path string, example int) field {
	return Field[int]{name, ValueCount, Source[int]{SourceRuntime, path}, example}
}

func durationField(name, path string, example time.Duration) field {
	return Field[time.Duration]{name, ValueDuration, Source[time.Duration]{SourceRuntime, path}, example}
}

func listField(name, path string, example []string) field {
	return Field[[]string]{name, ValueList, Source[[]string]{SourceProtocol, path}, example}
}

func registry() []unit {
	units := []unit{
		{
			id: "launch.workspace-context", name: "Workspace context contract", moment: MomentLaunch,
			summary:    "Points a workspace agent at this session's checked-out shared context and defines the conflict rules.",
			trigger:    "Launch when a non-chief session has a context checkout.",
			conditions: []predicate{roleIsNot(RoleChief), contextPresent(), launchInstructionsCapable()}, delivery: launchDelivery, deliveries: launchChoices,
			copyPath: "workspace-context.md", sourcePath: "internal/hooks/hooks.go:49",
			fields: []field{pathField("context_path", "WorkspaceContextResult.Path", "/tmp/attn/context.md")}, load: embedded("workspace-context.md"),
		},
		{
			id: "launch.workflow", name: "Workflow opt-in", moment: MomentLaunch,
			summary:    "Explains the exact user-owned words that authorize a durable workflow.",
			trigger:    "Launch when workflows_enabled is true; never for workflow subagents.",
			conditions: []predicate{roleIsNot(RoleChief), workflowEnabled(), launchInstructionsCapable()}, delivery: launchDelivery, deliveries: launchChoices,
			copyPath: "workflow-trigger.md", sourcePath: "internal/hooks/hooks.go:69", load: embedded("workflow-trigger.md"),
		},
		{
			id: "launch.chief", name: "Chief-of-staff identity", moment: MomentLaunch,
			summary:    "Replaces workspace context guidance with the profile-wide Notebook and coordination contract.",
			trigger:    "Launch when NotebookGuide says the session is chief.",
			conditions: []predicate{roleIs(RoleChief), launchInstructionsCapable()}, delivery: launchDelivery, deliveries: launchChoices,
			copyPath: "chief.md", sourcePath: "internal/hooks/hooks.go:160",
			fields: []field{pathField("notebook_root", "NotebookGuideResult.Root", "/Users/victor/.attn/notebook")}, load: embedded("chief.md"),
		},
		{
			id: "launch.garden", name: "Standing garden guidance", moment: MomentLaunch,
			summary:    "The full standing garden contract moved here in #990; SessionStart carries only live state.",
			trigger:    "Every launch whose SeedReady call proves this daemon is home.",
			conditions: []predicate{home(), launchInstructionsCapable()}, delivery: launchDelivery, deliveries: launchChoices,
			copyPath: "garden-standing.md", sourcePath: "internal/hooks/hooks.go:82", load: embedded("garden-standing.md"),
		},
		{
			id: "launch.crew", name: "Crew priming", moment: MomentLaunch,
			summary:    "Names the member, points at its charter, inlines the freshest letter, and teaches closure.",
			trigger:    "Last launch block for a session bound to a crew member.",
			conditions: []predicate{home(), roleIs(RoleCrew), launchInstructionsCapable()}, delivery: launchDelivery, deliveries: launchChoices,
			copyPath: "crew-priming.md", sourcePath: "internal/crew/priming.go:63",
			fields: []field{
				textField("member_name", "crew.Member.ID via crew.DisplayName", "Trellis"),
				pathField("crew_home", "crew.Member.HomeDir", "/Users/victor/.attn/crew/trellis"),
				pathField("charter_path", "crew.Member.CharterPath", "/Users/victor/.attn/crew/trellis/CHARTER.md"),
				textField("handoff_name", "crew.Priming.HandoffName", "2026-08-22T21-04Z-trellis.md"),
				userMarkdownField("predecessor_letter", "crew.Priming.Handoff", "The registry shape is settled. Start with the scenario predicates."),
				listField("older_handoffs", "crew.Priming.OlderHandoffs", []string{"2026-08-21T18-30Z-trellis.md"}),
			}, load: embedded("crew-priming.md"),
		},
		{
			id: "hook.workspace-context-fallback", name: "Workspace context fallback", moment: MomentSessionStart,
			summary:    "Repeats the workspace contract only when launch could not carry it.",
			trigger:    "startup|resume|clear|compact, and no launch marker was exported.",
			conditions: []predicate{builtInAgent(), roleIsNot(RoleChief), contextPresent(), launchGuidanceMissing()}, delivery: fixedDelivery(DeliveryHookContext),
			copyPath: "workspace-context.md", sourcePath: "cmd/attn/main.go:3383",
			fields: []field{pathField("context_path", "WorkspaceContextResult.Path", "/tmp/attn/context.md")}, load: embedded("workspace-context.md"),
		},
		{
			id: "hook.garden-no-plot", name: "Garden tail: no plot", moment: MomentSessionStart,
			summary:    "Refreshes the garden-wide ready count without repeating standing guidance.",
			trigger:    "startup|resume|clear|compact when flag-free ready has no crown.",
			conditions: []predicate{home(), builtInAgent(), gardenIs(GardenNoPlot)}, delivery: fixedDelivery(DeliveryHookContext),
			copyPath: "garden-tail-no-plot.md", sourcePath: "cmd/attn/seed.go:227",
			fields: []field{countField("ready_count", "len(SeedReadyResult.Seeds)", 3)}, load: embedded("garden-tail-no-plot.md"),
		},
		{
			id: "hook.garden-at-seed", name: "Garden tail: dispatched seed", moment: MomentSessionStart,
			summary:    "Points a delegate back to the exact seed it tends.",
			trigger:    "startup|resume|clear|compact when Crown has no PlotProgress.",
			conditions: []predicate{home(), builtInAgent(), gardenIs(GardenAtSeed)}, delivery: fixedDelivery(DeliveryHookContext),
			copyPath: "garden-tail-at-seed.md", sourcePath: "cmd/attn/seed.go:238",
			fields: []field{idField("seed_id", "SeedReadyResult.Crown.ID", "s-7k3f9m"), textField("seed_title", "SeedReadyResult.Crown.Title", "Typed registry spike")}, load: embedded("garden-tail-at-seed.md"),
		},
		{
			id: "hook.garden-at-plot", name: "Garden tail: active plot", moment: MomentSessionStart,
			summary:    "Names the crown and lists ready children with their freshest handoffs.",
			trigger:    "startup|resume|clear|compact when the bound plot has ready children.",
			conditions: []predicate{home(), builtInAgent(), gardenIs(GardenAtPlot)}, delivery: fixedDelivery(DeliveryHookContext),
			copyPath: "garden-tail-at-plot.md", sourcePath: "cmd/attn/seed.go:245",
			fields: []field{
				idField("plot_id", "SeedReadyResult.Crown.ID", "s-rtkmjm"), textField("plot_title", "SeedReadyResult.Crown.Title", "Guidance map"),
				listField("ready_seeds", "SeedReadyResult.Seeds", []string{"s-a1b2c3 Registry", "s-d4e5f6 Browser"}),
				userMarkdownField("freshest_handoffs", "SeedReadyResult.Handoffs", "Start with the registry validation test."),
			}, load: embedded("garden-tail-at-plot.md"),
		},
		{
			id: "hook.garden-empty-plot", name: "Garden tail: empty plot", moment: MomentSessionStart,
			summary:    "Says the plot has no ready work and points at the dependency view.",
			trigger:    "startup|resume|clear|compact when the bound plot has no ready children.",
			conditions: []predicate{home(), builtInAgent(), gardenIs(GardenEmptyPlot)}, delivery: fixedDelivery(DeliveryHookContext),
			copyPath: "garden-tail-empty-plot.md", sourcePath: "cmd/attn/seed.go:242",
			fields: []field{idField("plot_id", "SeedReadyResult.Crown.ID", "s-rtkmjm")}, load: embedded("garden-tail-empty-plot.md"),
		},
		{
			id: "delegate.leaf", name: "Delegated leaf identity", moment: MomentFirstPrompt,
			summary:    "Prevents an ordinary delegated session from mistaking itself for a coordinator.",
			trigger:    "Prepended to every delegated initial prompt.",
			conditions: []predicate{roleIs(RoleDelegate), initialPromptCapable()}, delivery: fixedDelivery(DeliveryInitial),
			copyPath: "delegate-leaf.md", sourcePath: "internal/daemon/delegate.go:966", load: embedded("delegate-leaf.md"),
		},
		{
			id: "delegate.brief", name: "Delegate brief", moment: MomentFirstPrompt,
			summary: "The caller's task, unchanged apart from surrounding composition separators.",
			trigger: "Every delegation.", conditions: []predicate{roleIs(RoleDelegate), initialPromptCapable()}, delivery: fixedDelivery(DeliveryInitial),
			copyPath: "delegate-brief.md", sourcePath: "internal/daemon/delegate.go:990",
			fields: []field{userMarkdownField("brief", "DelegateMessage.Brief", "Audit the current guidance paths and build the typed map.")}, load: embedded("delegate-brief.md"),
		},
		{
			id: "delegate.seed-reporting", name: "Delegate seed reporting", moment: MomentFirstPrompt,
			summary:    "The trimmed #990 tail: show, note, and harvest timing only.",
			trigger:    "Delegation when the home garden bound a seed; absent on an outpost.",
			conditions: []predicate{home(), roleIs(RoleDelegate), initialPromptCapable()}, delivery: fixedDelivery(DeliveryInitial),
			copyPath: "delegate-seed-reporting.md", sourcePath: "internal/daemon/delegate.go:988",
			fields: []field{idField("seed_id", "gardenDispatch.SeedID", "s-rtkmjm")}, load: embedded("delegate-seed-reporting.md"),
		},
		{
			id: "crew.cold-wake", name: "Crew cold wake", moment: MomentFirstPrompt,
			summary:    "Asks a freshly woken member to orient and greet Victor.",
			trigger:    "User, sidebar, or addressed message wakes a sleeping member.",
			conditions: []predicate{home(), roleIs(RoleCrew), initialPromptCapable()}, delivery: fixedDelivery(DeliveryInitial),
			copyPath: "crew-cold-wake.md", sourcePath: "internal/daemon/crew_wake.go:77", load: embedded("crew-cold-wake.md"),
		},
		{
			id: "crew.message-wake", name: "Crew addressed-message wake", moment: MomentFirstPrompt,
			summary:    "Replaces the ordinary greeting with the message that caused a sleeping member to wake.",
			trigger:    "An addressed agent message arrives while the crew member is asleep.",
			conditions: []predicate{home(), roleIs(RoleCrew), initialPromptCapable()}, delivery: fixedDelivery(DeliveryInitial),
			copyPath: "crew-message-wake.md", sourcePath: "internal/daemon/crew_wake.go:87",
			fields: []field{userMarkdownField("agent_message", "crewWakeDelivery.Prompt", "Victor asks: check the guidance map before the next review.")}, load: embedded("crew-message-wake.md"),
		},
		{
			id: "crew.nap-wake", name: "Crew successor wake", moment: MomentHandoff,
			summary:    "Starts the next day from the letter just filed, never from a resumed transcript.",
			trigger:    "A handoff closes with nap, or presence decides immediate turnover.",
			conditions: []predicate{home(), roleIs(RoleCrew), initialPromptCapable()}, delivery: fixedDelivery(DeliveryInitial),
			copyPath: "crew-nap-wake.md", sourcePath: "internal/daemon/crew_handoff.go:40", load: embedded("crew-nap-wake.md"),
		},
		{
			id: "nudge.heartbeat", name: "Cache heartbeat", moment: MomentNudge,
			summary:    "Asks for one short line solely to refresh the member's prompt cache.",
			trigger:    "Reachable, settled crew session; cache inside lead; user present; heartbeat enabled; not mid-turn.",
			conditions: []predicate{home(), roleIs(RoleCrew)}, delivery: nudgeDelivery,
			copyPath: "crew-heartbeat.md", sourcePath: "internal/daemon/crew_lifecycle.go:131", load: embedded("crew-heartbeat.md"),
		},
		{
			id: "nudge.auto-sleep", name: "Presence-decided sleep", moment: MomentNudge,
			summary:    "Ends a warm day when the user has crossed the measured away limit.",
			trigger:    "Reachable crew session; cache inside lead; user away beyond limit; auto-sleep enabled; not mid-turn.",
			conditions: []predicate{home(), roleIs(RoleCrew)}, delivery: nudgeDelivery,
			copyPath: "crew-auto-sleep.md", sourcePath: "internal/daemon/crew_lifecycle.go:137", load: embedded("crew-auto-sleep.md"),
		},
		{
			id: "nudge.context-handoff", name: "Context-budget handoff", moment: MomentNudge,
			summary:    "Interrupts before harness compaction and names the measured tokens and budget.",
			trigger:    "Reachable crew session at or above its context budget; may fire mid-turn; once per full episode.",
			conditions: []predicate{home(), roleIs(RoleCrew)}, delivery: nudgeDelivery,
			copyPath: "crew-context-handoff.md", sourcePath: "internal/daemon/crew_lifecycle.go:141",
			fields: []field{countField("tokens", "transcript.ContextObservation.Tokens", 160842), countField("budget", "crew.ContextPressure.Budget", 160000)}, load: embedded("crew-context-handoff.md"),
		},
		{
			id: "nudge.requested-sleep", name: "User-requested crew sleep", moment: MomentNudge,
			summary:    "Carries Victor's request through the durable doorbell without pretending another agent sent it.",
			trigger:    "attn crew sleep for a member whose bound session is live.",
			conditions: []predicate{home(), roleIs(RoleCrew)}, delivery: nudgeDelivery,
			copyPath: "crew-requested-sleep.md", sourcePath: "internal/daemon/crew_sleep.go:15", load: embedded("crew-requested-sleep.md"),
		},
		{
			id: "cli.seed-prime", name: "attn seed prime", moment: MomentCLI,
			summary:    "Prints the standing garden text plus the same live tail SessionStart selects.",
			trigger:    "Agent explicitly runs attn seed prime on a home daemon.",
			conditions: []predicate{home()}, delivery: fixedDelivery(DeliveryCLIStdout),
			copyPath: "garden-standing.md + selected garden-tail-*.md", sourcePath: "cmd/attn/seed.go:264",
			fields: []field{
				textField("standing_garden_guidance", "hooks.GardenGuidance", "[standing garden guidance above]"),
				textField("selected_live_tail", "seedPrimeTailFromReady(SeedReadyResult)", "You were dispatched to work at plot `s-rtkmjm`..."),
			}, load: embedded("seed-prime.md"),
		},
		{
			id: "cli.seed-guide", name: "attn seed guide", moment: MomentCLI,
			summary: "The on-demand craft for bodies, plans, evidence, artifacts, and handoffs.",
			trigger: "Agent explicitly runs attn seed guide.", conditions: []predicate{always()}, delivery: fixedDelivery(DeliveryCLIStdout),
			copyPath: "seed-guide.md", sourcePath: "cmd/attn/seed_guide.go:28", load: embedded("seed-guide.md"),
		},
		{
			id: "refusal.outpost", name: "Home ownership refusal", moment: MomentCLI,
			summary:    "Names the outpost, its home, why the command stopped, and both ways forward.",
			trigger:    "Any garden or crew command reaches enrollment.Status.RequireHome on an outpost.",
			conditions: []predicate{outpost()}, delivery: fixedDelivery(DeliveryCLIStdout),
			copyPath: "refusal-outpost.md", sourcePath: "internal/enrollment/enrollment.go:89",
			fields: []field{textField("surface", "FencedError.Surface", "the garden"), idField("daemon_id", "FencedError.DaemonID", "d-outpost"), idField("home_daemon_id", "FencedError.HomeDaemonID", "d-home")}, load: embedded("refusal-outpost.md"),
		},
		{
			id: "refusal.seed-not-found", name: "Unknown seed refusal", moment: MomentCLI,
			summary:    "Names the missing id and the command that lists the garden.",
			trigger:    "A garden verb resolves an id absent from the seed collection.",
			conditions: []predicate{home()}, delivery: fixedDelivery(DeliveryCLIStdout),
			copyPath: "refusal-seed-not-found.md", sourcePath: "internal/daemon/garden.go:153",
			fields: []field{idField("seed_id", "SeedID argument", "s-7k3f9m")}, load: embedded("refusal-seed-not-found.md"),
		},
		{
			id: "refusal.seed-taken", name: "Taken seed refusal", moment: MomentCLI,
			summary:    "Names the current tender and the force escape hatch.",
			trigger:    "A lifecycle move targets a seed still held by another live tender.",
			conditions: []predicate{home()}, delivery: fixedDelivery(DeliveryCLIStdout),
			copyPath: "refusal-seed-taken.md", sourcePath: "internal/garden/lifecycle.go:180",
			fields: []field{idField("seed_id", "garden.Seed.ID", "s-7k3f9m"), textField("tender", "garden.Tender.DisplayName", "Trellis")}, load: embedded("refusal-seed-taken.md"),
		},
		{
			id: "refusal.ticket-retired", name: "Retired ticket signpost", moment: MomentCLI,
			summary: "Routes every old ticket write verb to its garden replacement while keeping reads discoverable.",
			trigger: "Agent runs a retired attn ticket write verb.", conditions: []predicate{always()}, delivery: fixedDelivery(DeliveryCLIStdout),
			copyPath: "refusal-ticket-retired.md", sourcePath: "cmd/attn/ticket_signpost.go:123",
			fields: []field{textField("ticket_verb", "ticketSignposts map key", "status"), textField("garden_command", "ticketSignpost.Moves", "attn seed note <id> -m <update>")}, load: embedded("refusal-ticket-retired.md"),
		},
		{
			id: "refusal.wake-limit", name: "Autonomous wake limit", moment: MomentCLI,
			summary:    "A visible tripwire that names the measured window, current count, limit, and remedy.",
			trigger:    "An unattended nap or message wake would exceed the member's wake ledger.",
			conditions: []predicate{home(), roleIs(RoleCrew)}, delivery: fixedDelivery(DeliveryCLIStdout),
			copyPath: "refusal-wake-limit.md", sourcePath: "internal/crew/lifecycle.go:286",
			fields: []field{textField("member_name", "crew.Member.ID via DisplayName", "Trellis"), countField("wake_count", "len(WakeLedger.Within(now))", 8), durationField("window", "WakeLedger.Window", 12*time.Hour), countField("limit", "WakeLedger.Limit", 8)}, load: embedded("refusal-wake-limit.md"),
		},
	}

	units = append(units, skillUnits()...)
	return units
}

func skillUnits() []unit {
	paths := []string{"SKILL.md"}
	for _, name := range agentdriver.SkillReferenceNames() {
		paths = append(paths, "references/"+name+".md")
	}
	sort.Strings(paths)
	units := make([]unit, 0, len(paths))
	for _, path := range paths {
		id := "skill." + strings.TrimSuffix(strings.TrimPrefix(path, "references/"), ".md")
		if path == "SKILL.md" {
			id = "skill.index"
		}
		units = append(units, unit{
			id: id, name: "attn skill: " + path, moment: MomentCLI,
			summary:    "Bundled markdown read on demand through the attn skill router.",
			trigger:    "The agent loads this attn skill file for the capability it needs.",
			conditions: []predicate{always()}, delivery: fixedDelivery(DeliverySkillRead),
			copyPath:   "internal/agent/attn_skill/" + path,
			sourcePath: "internal/agent/attn_skill/" + path,
			load:       skill(path),
		})
	}
	return units
}

func compositions() []composition {
	return []composition{
		{
			id: "system-prompt", name: "Launch instructions", moment: MomentLaunch,
			trigger:    "One ordered string; blank blocks are omitted and crew is always last.",
			conditions: []predicate{launchInstructionsCapable()}, delivery: launchDelivery,
			blocks: []string{"launch.chief", "launch.workspace-context", "launch.workflow", "launch.garden", "launch.crew"},
		},
		{
			id: "session-start", name: "SessionStart additionalContext", moment: MomentSessionStart,
			trigger:    "startup|resume|clear|compact; plugin agents have no native hook here.",
			conditions: []predicate{builtInAgent()}, delivery: fixedDelivery(DeliveryHookContext),
			blocks: []string{"hook.workspace-context-fallback", "hook.garden-no-plot", "hook.garden-at-seed", "hook.garden-at-plot", "hook.garden-empty-plot"},
		},
		{
			id: "delegate-initial", name: "Delegate first prompt", moment: MomentFirstPrompt,
			trigger:    "Leaf identity, caller brief, then the trimmed seed-reporting tail.",
			conditions: []predicate{roleIs(RoleDelegate), initialPromptCapable()}, delivery: fixedDelivery(DeliveryInitial),
			blocks: []string{"delegate.leaf", "delegate.brief", "delegate.seed-reporting"},
		},
		{
			id: "crew-cold-start", name: "Crew cold day", moment: MomentFirstPrompt,
			trigger:    "System prompt carries priming; first prompt asks the member to orient.",
			conditions: []predicate{home(), roleIs(RoleCrew), initialPromptCapable()}, delivery: fixedDelivery(DeliveryInitial),
			blocks: []string{"launch.crew", "crew.cold-wake", "crew.message-wake"},
		},
		{
			id: "crew-turnover", name: "Crew nap turnover", moment: MomentHandoff,
			trigger:    "Filed letter becomes next launch priming; successor gets a fresh initial prompt.",
			conditions: []predicate{home(), roleIs(RoleCrew), initialPromptCapable()}, delivery: fixedDelivery(DeliveryInitial),
			blocks: []string{"launch.crew", "crew.nap-wake"},
		},
		{
			id: "seed-prime", name: "attn seed prime output", moment: MomentCLI,
			trigger:    "Standing copy followed by exactly one live tail.",
			conditions: []predicate{home()}, delivery: fixedDelivery(DeliveryCLIStdout),
			blocks: []string{"launch.garden", "hook.garden-no-plot", "hook.garden-at-seed", "hook.garden-at-plot", "hook.garden-empty-plot"},
		},
	}
}

func lifecycle() []LifecycleLane {
	return []LifecycleLane{
		{ID: "plain", Name: "Plain session", Steps: []LifecycleStep{
			{ID: "plain-launch", Label: "Launch", Moment: MomentLaunch, UnitIDs: []string{"launch.workspace-context", "launch.workflow", "launch.garden"}, Next: []string{"plain-hook"}},
			{ID: "plain-hook", Label: "startup / resume / clear / compact", Moment: MomentSessionStart, UnitIDs: []string{"hook.workspace-context-fallback", "hook.garden-no-plot", "hook.garden-at-seed", "hook.garden-at-plot", "hook.garden-empty-plot"}},
		}},
		{ID: "delegate", Name: "Delegate", Steps: []LifecycleStep{
			{ID: "delegate-bind", Label: "Bind seed", Moment: MomentCLI, UnitIDs: nil, Next: []string{"delegate-launch"}},
			{ID: "delegate-launch", Label: "Launch + leaf first prompt", Moment: MomentFirstPrompt, UnitIDs: []string{"launch.workspace-context", "launch.garden", "delegate.leaf", "delegate.brief", "delegate.seed-reporting"}, Next: []string{"delegate-hook"}},
			{ID: "delegate-hook", Label: "Context reset", Moment: MomentSessionStart, UnitIDs: []string{"hook.garden-at-seed", "hook.garden-at-plot", "hook.garden-empty-plot"}},
		}},
		{ID: "crew", Name: "Crew day", Steps: []LifecycleStep{
			{ID: "crew-wake", Label: "Wake fresh or by message", Moment: MomentFirstPrompt, UnitIDs: []string{"launch.crew", "crew.cold-wake", "crew.message-wake"}, Next: []string{"crew-day"}},
			{ID: "crew-day", Label: "Work / wait", Moment: MomentNudge, UnitIDs: []string{"nudge.heartbeat", "nudge.auto-sleep", "nudge.context-handoff", "nudge.requested-sleep"}, Next: []string{"crew-letter"}},
			{ID: "crew-letter", Label: "File letter", Moment: MomentHandoff, UnitIDs: nil, Next: []string{"crew-sleep", "crew-successor"}},
			{ID: "crew-sleep", Label: "Sleep", Moment: MomentHandoff, UnitIDs: nil},
			{ID: "crew-successor", Label: "Fresh successor", Moment: MomentHandoff, UnitIDs: []string{"launch.crew", "crew.nap-wake"}, Next: []string{"crew-day"}},
		}},
	}
}

func eligible(conditions []predicate, s Scenario) bool {
	for _, condition := range conditions {
		if !condition.match(s) {
			return false
		}
	}
	return true
}

func conditionViews(conditions []predicate, s Scenario) []ConditionView {
	views := make([]ConditionView, 0, len(conditions))
	for _, condition := range conditions {
		views = append(views, ConditionView{condition.label, condition.source, condition.match(s)})
	}
	return views
}

func expand(copy string, fields []PlaceholderView) string {
	for _, field := range fields {
		copy = strings.ReplaceAll(copy, "{{"+field.Name+"}}", field.Example)
	}
	return copy
}

// Catalog walks the Go registry and returns the complete static map plus the
// selected scenario's predicate results.
func Catalog(s Scenario) (Snapshot, error) {
	s = s.normalized()
	entries := registry()
	unitViews := make([]UnitView, 0, len(entries))
	byID := make(map[string]UnitView, len(entries))
	for _, entry := range entries {
		copy, err := entry.load()
		if err != nil {
			return Snapshot{}, fmt.Errorf("load %s: %w", entry.id, err)
		}
		placeholders := make([]PlaceholderView, 0, len(entry.fields))
		for _, field := range entry.fields {
			placeholders = append(placeholders, field.definition())
		}
		view := UnitView{
			ID: entry.id, Name: entry.name, Summary: entry.summary, Moment: entry.moment,
			Trigger: entry.trigger, Eligible: eligible(entry.conditions, s), Delivery: entry.delivery(s),
			DeliveryChoices: entry.deliveries, Conditions: conditionViews(entry.conditions, s),
			Copy: copy, Preview: expand(copy, placeholders), CopyPath: entry.copyPath,
			SourcePath: entry.sourcePath, Placeholders: placeholders,
		}
		unitViews = append(unitViews, view)
		byID[view.ID] = view
	}

	compositionViews := make([]CompositionView, 0, len(compositions()))
	for _, item := range compositions() {
		blocks := make([]BlockView, 0, len(item.blocks))
		for index, id := range item.blocks {
			unit, ok := byID[id]
			if !ok {
				return Snapshot{}, fmt.Errorf("composition %s references unknown unit %s", item.id, id)
			}
			blocks = append(blocks, BlockView{index + 1, unit.ID, unit.Name, unit.Eligible})
		}
		compositionViews = append(compositionViews, CompositionView{
			ID: item.id, Name: item.name, Moment: item.moment, Trigger: item.trigger,
			Eligible: eligible(item.conditions, s), Delivery: item.delivery(s),
			Conditions: conditionViews(item.conditions, s), Blocks: blocks,
		})
	}

	return Snapshot{
		Scenario: s,
		Axes:     AxesView{[]Agent{AgentClaude, AgentCodex, AgentPlugin}, []Role{RolePlain, RoleDelegate, RoleCrew, RoleChief}, []GardenState{GardenNoPlot, GardenAtSeed, GardenAtPlot, GardenEmptyPlot}},
		Units:    unitViews, Compositions: compositionViews,
		Moments:   []Moment{MomentLaunch, MomentSessionStart, MomentFirstPrompt, MomentNudge, MomentHandoff, MomentCLI},
		Lifecycle: lifecycle(),
	}, nil
}

var placeholderPattern = regexp.MustCompile(`\{\{([a-z][a-z0-9_]*)\}\}`)

// Validate proves the registry facts a compiler cannot: ids, composition
// references, copy files, and agreement between template tokens and fields.
func Validate() error {
	entries := registry()
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.id == "" || seen[entry.id] {
			return fmt.Errorf("duplicate or empty unit id %q", entry.id)
		}
		seen[entry.id] = true
		copy, err := entry.load()
		if err != nil || strings.TrimSpace(copy) == "" {
			return fmt.Errorf("unit %s has no readable copy at %s: %v", entry.id, entry.copyPath, err)
		}
		declared := map[string]bool{}
		for _, field := range entry.fields {
			definition := field.definition()
			if declared[definition.Name] {
				return fmt.Errorf("unit %s declares placeholder %s twice", entry.id, definition.Name)
			}
			declared[definition.Name] = true
		}
		found := map[string]bool{}
		for _, match := range placeholderPattern.FindAllStringSubmatch(copy, -1) {
			found[match[1]] = true
		}
		for name := range found {
			if !declared[name] {
				return fmt.Errorf("unit %s uses undeclared placeholder %s", entry.id, name)
			}
		}
		for name := range declared {
			if !found[name] {
				return fmt.Errorf("unit %s declares unused placeholder %s", entry.id, name)
			}
		}
	}
	for _, composition := range compositions() {
		if composition.id == "" || seen["composition:"+composition.id] {
			return fmt.Errorf("duplicate or empty composition id %q", composition.id)
		}
		seen["composition:"+composition.id] = true
		for _, block := range composition.blocks {
			if !seen[block] {
				return fmt.Errorf("composition %s references unknown unit %s", composition.id, block)
			}
		}
	}
	return nil
}
