package prompts

import (
	"embed"
	"io/fs"
	"strconv"
	"sync"
)

//go:embed content
var content embed.FS

var (
	selfReportPullRequests = FlagField("self_report_pull_requests", "Ask the agent to record PRs when the harness cannot report them.")
	gardenAvailable        = FlagField("garden_available", "Include Garden guidance.")
	asksEnabled            = FlagField("asks_enabled", "Include ask guidance when the session launched with asks.")
	ventEnabled            = FlagField("vent_enabled", "Include vent guidance when the session launched with usage analytics on.")
	toolVentsEnabled       = FlagField("tool_vents_enabled", "Offer tool vents when the session launched with analytics.tool_vents on.")
	codeReviewEnabled      = FlagField("code_review_enabled", "List presenting a change for review when the session launched with code_review_enabled on.")
	crewPriming            = ProducedBy(Trimmed(TextField("crew_priming", "Rendered crew identity and predecessor letter, supplied by internal/crew.")), "crew/priming")
)

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
	bullets := []Node{attnMapPart("skill"), attnMapPart("guide")}
	if launch {
		bullets = []Node{cliTeaching(bullets[0], "skill"), cliTeaching(bullets[1], "guide")}
	}
	parts := append([]Node{head}, bullets...)
	parts = append(parts, attnMapPart("table"))
	parts = append(parts, attnMapTable(attnMapRows, launch)...)
	if launch {
		parts = append(parts, When(Enabled(codeReviewEnabled), Join("", attnMapTable([]attnMapRow{{"review", []string{"review"}}, {"nav", []string{"nav"}}}, true)...)))
	}
	return Trim(Join("", parts...))
}

var (
	delegationBoundary = Use("delegation.boundary", "content/delegation-boundary.md")
	agentGuidance      = Compose(
		Use("session.agent", "content/agent.md"),
		cliTeaching(Use("session.agent-reach", "content/agent-reach.md"), "agent", "session"),
		Use("session.agent-trust", "content/agent-trust.md"),
		delegationBoundary)
	quickCaptureGuidance = Use("session.quick-capture-guidance", "content/session/quick-capture.md")
	crewCleared          = Use("crew.cleared", "content/crew/cleared.md")
	gardenRules          = Use("session.garden", "content/garden.md")
	gardenCLI            = Use("session.garden-cli", "content/garden-cli.md")
	gardenGuidance       = Compose(gardenRules, gardenCLI)
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
				When(Enabled(gardenAvailable), Compose(gardenRules, cliTeaching(gardenCLI, "seed"))),
				cliTeaching(queueGuidance, "turn", "agent"),
				When(Enabled(asksEnabled), cliTeaching(askGuidance, "ask")),
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
	Asks                   bool
	Vent                   bool
	ToolVents              bool
	CodeReview             bool
	Crew                   string
	AgentTools             bool
}

func (l Launch) Values() Values {
	return Values{
		selfReportPullRequests.Name: strconv.FormatBool(l.SelfReportPullRequests),
		gardenAvailable.Name:        strconv.FormatBool(l.Garden),
		asksEnabled.Name:            strconv.FormatBool(l.Asks),
		ventEnabled.Name:            strconv.FormatBool(l.Vent),
		toolVentsEnabled.Name:       strconv.FormatBool(l.ToolVents),
		codeReviewEnabled.Name:      strconv.FormatBool(l.CodeReview),
		crewPriming.Name:            l.Crew,
		agentToolsCarried.Name:      strconv.FormatBool(l.AgentTools),
	}
}

func (l Launch) Instructions() string {
	result, err := Builtin().Render("session", "launch", l.Values())
	if err != nil {
		panic(err)
	}
	return result.Text
}
