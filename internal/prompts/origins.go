package prompts

var (
	brief            = TextField("brief", "Configured task or delegation brief.")
	seedID           = TextField("seed_id", "Reporting seed, if one is bound to the session.")
	localReview      = FlagField("local_review", "The automation permits only a local review.")
	hasTarget        = FlagField("has_target", "A local review has structured pull request identity.")
	inputPath        = TextField("input_path", "Path to untrusted occurrence data.")
	leafIdentity     = Use("delegation.identity", "content/delegation/identity.md")
	seedContract     = Use("delegation.seed", "content/delegation/seed.md", Bind("seed_id", Input(seedID)))
	automationTarget = template("automation.target", "content/automation/target.md",
		TextField("definition", "Quoted automation name."),
		TextField("repository", "Repository identity."), TextField("number", "Pull request number."),
		TextField("url", "Pull request URL."), TextField("head_sha", "Checked-out head."), inputPath)
)

func template(id, source string, fields ...Field) Node {
	bindings := make([]Binding, 0, len(fields))
	for _, field := range fields {
		bindings = append(bindings, Bind(field.Name, Input(field)))
	}
	return Use(id, source, bindings...)
}

func delegatedBrief(body Node) Node {
	return Compose(Trim(body), When(Present(seedID), seedContract))
}

func leafOpening(body Node) Node {
	return Use("delegation.opening", "content/delegation/opening.md",
		Bind("identity", leafIdentity), Bind("brief", Trim(body)))
}

func originRecipients() []Recipient {
	return []Recipient{
		{ID: "delegation", Description: "Visible delegated session; appended after launch instructions.", Events: []Event{
			On("handover", "opening_message", "Existing seed assigned to a new agent.", leafOpening(seedContract)),
			On("handover-brief", "message_fragment", "Seed reference for a handover opening.", seedContract),
			On("opening", "opening_message", "Delegation identity and seed reference.", leafOpening(seedContract)),
			On("brief", "message_fragment", "Seed reference for the delegated opening.", seedContract),
			On("identity", "message_fragment", "Delegation identity shared by delegation and automation.", leafOpening(Input(brief))),
		}},
		{ID: "automation", Description: "Scheduled or event-triggered visible session.", Events: []Event{
			On("opening", "opening_message", "Configured task, local-review restriction, reporting seed and occurrence input.",
				leafOpening(Join("",
					delegatedBrief(Join("", Input(brief), When(Enabled(localReview), Use("automation.local-review", "content/automation/local-review.md")))),
					Choose(Enabled(hasTarget),
						Join("\n\n---\n\n", Compose(), automationTarget),
						Use("automation.data", "content/automation/data.md", Bind("input_path", Input(inputPath)))),
				))),
			On("target", "message_fragment", "Authoritative pull request identity followed by the untrusted-data contract.", automationTarget),
		}},
		{ID: "session-instructions", Description: "Separate model answering questions about conversation evidence.", Events: []Event{
			On("question", "headless_prompt", "Labeled conversation and optional validation retry.",
				Use("session-instructions.question", "content/session-instructions/question.md",
					Bind("question", Input(TextField("question", "Question to answer."))),
					Bind("conversation", Input(TextField("conversation", "Labeled turns, including trailing newlines."))),
					Bind("retry", When(Enabled(FlagField("retry", "Retry after validation errors.")),
						template("session-instructions.retry", "content/session-instructions/retry.md", TextField("errors", "Validation errors separated by newline and dash.")))),
				)),
		}},
	}
}
