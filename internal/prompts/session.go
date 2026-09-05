package prompts

import (
	"embed"
	"io/fs"
	"strconv"
)

//go:embed content
var content embed.FS

var (
	notebookRoot           = Trimmed(TextField("notebook_root", "Notebook directory; a nonempty value selects chief guidance."))
	selfReportPullRequests = FlagField("self_report_pull_requests", "Ask the agent to record PRs when the harness cannot report them.")
	workflowEnabled        = FlagField("workflow_enabled", "Include opt-in workflow guidance for an ordinary session.")
	gardenAvailable        = FlagField("garden_available", "Include Garden guidance when the session has a home Garden.")
	crewPriming            = ProducedBy(Trimmed(TextField("crew_priming", "Rendered crew identity and predecessor letter, supplied by internal/crew.")), "crew/priming")
)

var (
	delegationBoundary = Use("delegation.boundary", "content/delegation-boundary.md")
	chiefGuidance      = Use("session.chief", "content/chief.md",
		Bind("notebook_root", Input(notebookRoot)),
		Bind("delegation_boundary", delegationBoundary))
	agentGuidance = Use("session.agent", "content/agent.md",
		Bind("delegation_boundary", delegationBoundary))
	workflowGuidance    = Use("session.workflow", "content/workflow.md")
	gardenGuidance      = Use("session.garden", "content/garden.md")
	pullRequestGuidance = Use("session.pull-request-guidance", "content/session/pull-request-guidance.md")
)

var session = Recipient{
	ID:          "session",
	Description: "Working session: launch instructions and later notifications. Harness-owned context remains outside this catalog.",
	Events: []Event{
		On("launch", "launch_instructions",
			"hooks.Launch.Instructions: Claude appends system instructions; Codex sets developer instructions; capable plugins receive launch instructions. Triggering and delivery remain with the existing adapters.",
			Compose(
				Choose(Present(notebookRoot),
					chiefGuidance,
					Compose(
						agentGuidance,
						When(Enabled(workflowEnabled), workflowGuidance),
					)),
				When(Enabled(gardenAvailable), gardenGuidance),
				When(Enabled(selfReportPullRequests), pullRequestGuidance),
				When(Present(crewPriming), Input(crewPriming)),
			)),
		On("agent-guidance", "message_fragment", "Non-chief trust and delegation guidance.", agentGuidance),
		On("garden-guidance", "message_fragment", "Garden instructions when a home is available.", gardenGuidance),
		On("workflow-guidance", "message_fragment", "Opt-in workflow instructions.", workflowGuidance),
		On("pull-request-guidance", "message_fragment", "Self-report instructions for harnesses without automatic PR reporting.", pullRequestGuidance),
	},
}

var builtin = mustCatalog()

func mustCatalog() *Catalog {
	catalog, err := Load(content)
	if err != nil {
		panic(err)
	}
	return catalog
}

func Builtin() *Catalog { return builtin }

func Load(files fs.FS) (*Catalog, error) { return New(files, Definitions()...) }
func Files() fs.FS                       { return content }

type Launch struct {
	NotebookRoot           string
	SelfReportPullRequests bool
	InjectWorkflow         bool
	Garden                 bool
	Crew                   string
}

func (l Launch) Values() Values {
	return Values{
		notebookRoot.Name:           l.NotebookRoot,
		selfReportPullRequests.Name: strconv.FormatBool(l.SelfReportPullRequests),
		workflowEnabled.Name:        strconv.FormatBool(l.InjectWorkflow),
		gardenAvailable.Name:        strconv.FormatBool(l.Garden),
		crewPriming.Name:            l.Crew,
	}
}

func (l Launch) Instructions() string {
	result, err := builtin.Render("session", "launch", l.Values())
	if err != nil {
		panic(err)
	}
	return result.Text
}
