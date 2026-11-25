package prompts

import (
	"embed"
	"io/fs"
	"slices"
	"strconv"
	"sync"
)

//go:embed content
var content embed.FS

var (
	selfReportPullRequests = FlagField("self_report_pull_requests", "Ask the agent to record PRs when the harness cannot report them.")
	gardenAvailable        = FlagField("garden_available", "Include Garden guidance.")
	gardenV2Enabled        = FlagField("garden_v2_enabled", "Include Garden v2 beds and pulse guidance when garden_v2_enabled is on.")
	gardenBeds             = Trimmed(TextField("garden_beds", "Comma-separated curated bed names in the session's profile when garden_v2_enabled is on."))
	asksEnabled            = FlagField("asks_enabled", "Include ask guidance when the session launched with asks.")
	ventEnabled            = FlagField("vent_enabled", "Include vent guidance when the session launched with usage analytics on.")
	toolVentsEnabled       = FlagField("tool_vents_enabled", "Offer tool vents when the session launched with analytics.tool_vents on.")
	codeReviewEnabled      = FlagField("code_review_enabled", "List presenting a change for review when the session launched with code_review_enabled on.")
	crewPriming            = ProducedBy(Trimmed(TextField("crew_priming", "Rendered crew identity and predecessor letter, supplied by internal/crew.")), "crew/priming")
)

var claudeCodePeerTools = FlagField("claude_code_peer_tools", "The session's harness is Claude Code, whose own tools list and message Claude Code sessions.")

var agentToolsCarried = FlagField("agent_tools", "The session's harness received attn's tools, so the launch leaves out the CLI teaching of every family the tools offer.")

func cliTeaching(node Node, families ...string) Node {
	for _, family := range families {
		offered, known := catalogFamilies[family]
		if !known {
			panic("prompts: the catalog has no family " + strconv.Quote(family))
		}
		if !offered {
			return node
		}
	}
	return Choose(Enabled(agentToolsCarried), Compose(), node)
}

func attnMapPart(name string) Node {
	return Exact(Use("session.attn-map."+name, "content/session/attn-map/"+name+".md"))
}

type attnMapRow struct {
	name     string
	families []string
}

var attnMapRows = []attnMapRow{
	{"seed-track", []string{"seed"}},
	{"delegate", []string{"delegate"}},
	{"seed-report", []string{"seed"}},
	{"pr", []string{"pr"}},
	{"agent", []string{"agent"}},
	{"session", []string{"session"}},
	{"seed-standby", []string{"seed"}},
	{"terminal", []string{"terminal"}},
	{"open", []string{"open"}},
	{"showing", []string{"skill"}},
	{"browser", []string{"browser"}},
	{"callout", []string{"callout"}},
	{"journal", []string{"journal"}},
	{"crew", []string{"crew"}},
	{"automation", []string{"automation"}},
	{"settings", []string{"settings"}},
}

func attnMapFamilies(rows []attnMapRow) []string {
	var families []string
	for _, row := range rows {
		families = append(families, row.families...)
	}
	return families
}

func attnMapTable(rows []attnMapRow, launch bool) []Node {
	nodes := make([]Node, 0, len(rows))
	for _, row := range rows {
		if launch {
			nodes = append(nodes, cliTeaching(attnMapPart(row.name), row.families...))
		} else {
			nodes = append(nodes, attnMapPart(row.name))
		}
	}
	return nodes
}

func attnMapNode(launch bool) Node {
	head := Exact(Use("session.attn-map", "content/session/attn-map.md"))
	bullets := []Node{attnMapPart("cli"), attnMapPart("skill"), attnMapPart("guide")}
	if launch {
		bullets = []Node{Choose(Enabled(agentToolsCarried), attnMapPart("tools"), bullets[0]), cliTeaching(bullets[1], "skill"), cliTeaching(bullets[2], "guide")}
	}
	parts := append([]Node{head}, bullets...)
	if !launch {
		return Trim(Join("", append(append(parts, attnMapPart("table")), attnMapTable(attnMapRows, false)...)...))
	}
	reviewRows := []attnMapRow{{"review", []string{"review"}}, {"nav", []string{"nav"}}}
	parts = append(parts, cliTeaching(attnMapPart("table"), attnMapFamilies(append(slices.Clone(attnMapRows), reviewRows...))...))
	parts = append(parts, attnMapTable(attnMapRows, true)...)
	parts = append(parts, When(Enabled(codeReviewEnabled), Join("", attnMapTable(reviewRows, true)...)))
	return Trim(Join("", parts...))
}

func forSurface(id, path string) Node {
	return Choose(Enabled(agentToolsCarried), Use(id+"-tools", path+"-tools.md"), Use(id+"-cli", path+"-cli.md"))
}

func gardenRulesWith(words func(id, path string) Node) Node {
	bind := func(name string) Binding {
		return Bind(name, words("session.garden."+name, "content/garden/"+name))
	}
	return Use("session.garden", "content/garden.md", bind("ids"), bind("guide"), bind("search"), bind("planting"))
}

func cliWords(id, path string) Node { return Use(id+"-cli", path+"-cli.md") }

var (
	delegationBoundary = Use("delegation.boundary", "content/delegation-boundary.md", Bind("read_reference", Choose(Enabled(agentToolsCarried),
		Use("delegation.boundary-tools", "content/delegation-boundary/tools.md"),
		Use("delegation.boundary-cli", "content/delegation-boundary/cli.md"))))
	agentGuidance = Compose(
		forSurface("session.agent", "content/agent"),
		cliTeaching(Use("session.agent-reach", "content/agent-reach.md"), "agent", "session"),
		When(Enabled(claudeCodePeerTools), Use("session.claude-code-peer-tools", "content/claude-code-peer-tools.md")),
		Use("session.agent-trust", "content/agent-trust.md"),
		delegationBoundary)
	quickCaptureGuidance = Use("session.quick-capture-guidance", "content/session/quick-capture.md")
	crewCleared          = Use("crew.cleared", "content/crew/cleared.md")
	gardenRules          = gardenRulesWith(forSurface)
	gardenCLI            = Use("session.garden-cli", "content/garden-cli.md")
	gardenV2Rules        = Use("session.garden-v2", "content/garden-v2.md")
	gardenV2Beds         = Use("session.garden-v2-beds", "content/garden-v2-beds.md", Bind("beds", Input(gardenBeds)))
	gardenV2CLI          = Use("session.garden-v2-cli", "content/garden-v2-cli.md")
	gardenGuidance       = Compose(gardenRulesWith(cliWords), gardenCLI)
	queueGuidance        = Use("session.queue", "content/session/queue.md")
	attnMap              = attnMapNode(false)
	launchAttnMap        = attnMapNode(true)
	pullRequestGuidance  = Use("session.pull-request-guidance", "content/session/pull-request-guidance.md")
	ventGuidance         = Compose(
		Use("session.vent-guidance", "content/session/vent-guidance.md"),
		Choose(Enabled(toolVentsEnabled),
			Use("session.vent-tools", "content/session/vent-tools.md"),
			Use("session.vent-attn-only", "content/session/vent-attn-only.md")),
		Use("session.vent-rules", "content/session/vent-rules.md"))
)

var session = Recipient{
	ID:          "session",
	Description: "Working session: launch instructions and later notifications. Harness-owned context remains outside this catalog.",
	Events: []Event{
		On("launch", "launch_instructions",
			"hooks.Launch.Instructions: Claude appends system instructions; Codex sets developer instructions; capable plugins receive launch instructions. Triggering and delivery remain with the existing adapters.",
			Compose(
				When(Present(crewPriming), Compose(crewCleared, Input(crewPriming), quickCaptureGuidance)),
				launchAttnMap,
				agentGuidance,
				When(Enabled(gardenAvailable), Compose(
					gardenRules,
					cliTeaching(gardenCLI, "seed"),
					When(Enabled(gardenV2Enabled), Compose(
						gardenV2Rules,
						When(Present(gardenBeds), gardenV2Beds),
						cliTeaching(gardenV2CLI, "seed"),
					)),
				)),
				cliTeaching(queueGuidance, "turn", "agent"),
				When(Enabled(asksEnabled), Compose(askRules, cliTeaching(askCLI, "ask"))),
				When(Enabled(ventEnabled), cliTeaching(ventGuidance, "vent")),
				When(Enabled(selfReportPullRequests), cliTeaching(pullRequestGuidance, "pr")),
			)),
		On("attn-map", "cli_output", "attn skill and attn --help: what attn offers and where to learn more.", attnMap),
		On("seed-artifacts", "cli_output", "attn seed show, under a seed's artifacts.", Use("session.seed-artifacts", "content/session/seed-artifacts.md")),
		On("garden-guidance", "message_fragment", "Garden instructions.", gardenGuidance),
		On("codex-opened", "instructions",
			"Developer note appended to each new shared Codex conversation. It writes the conversation to disk at once; Codex resumes only conversations on disk, so a terminal reconnecting after a restart keeps it.",
			Use("session.codex-opened", "content/session/codex-opened.md")),
	},
}

var builtin = sync.OnceValue(mustCatalog)

func mustCatalog() *Catalog {
	catalog, err := Load(content)
	if err != nil {
		panic(err)
	}
	return catalog
}

func Builtin() *Catalog { return builtin() }

func Load(files fs.FS) (*Catalog, error) { return New(files, Definitions()...) }
func Files() fs.FS                       { return content }

type Launch struct {
	SelfReportPullRequests bool
	Garden                 bool
	GardenV2               bool
	GardenBeds             string
	Asks                   bool
	Vent                   bool
	ToolVents              bool
	CodeReview             bool
	Crew                   string
	AgentTools             bool
	ClaudeCodePeerTools    bool
}

func (l Launch) Values() Values {
	return Values{
		selfReportPullRequests.Name: strconv.FormatBool(l.SelfReportPullRequests),
		gardenAvailable.Name:        strconv.FormatBool(l.Garden),
		gardenV2Enabled.Name:        strconv.FormatBool(l.GardenV2),
		gardenBeds.Name:             l.GardenBeds,
		asksEnabled.Name:            strconv.FormatBool(l.Asks),
		ventEnabled.Name:            strconv.FormatBool(l.Vent),
		toolVentsEnabled.Name:       strconv.FormatBool(l.ToolVents),
		codeReviewEnabled.Name:      strconv.FormatBool(l.CodeReview),
		crewPriming.Name:            l.Crew,
		agentToolsCarried.Name:      strconv.FormatBool(l.AgentTools),
		claudeCodePeerTools.Name:    strconv.FormatBool(l.ClaudeCodePeerTools),
	}
}

func (l Launch) Instructions() string {
	result, err := Builtin().Render("session", "launch", l.Values())
	if err != nil {
		panic(err)
	}
	return result.Text
}
