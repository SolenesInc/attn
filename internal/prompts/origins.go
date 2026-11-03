package prompts

var (
	brief        = TextField("brief", "Configured task or delegation brief.")
	seedTitle    = TextField("seed_title", "Title of the session's stake.")
	seedID       = TextField("seed_id", "The session's stake, if it has one.")
	checkIDs     = TextField("check_ids", "The stake's checks, such as \"C3, C4 and the plot's shared C1\"; blank when it has none.")
	localReview  = FlagField("local_review", "The automation permits only a local review.")
	hasTarget    = FlagField("has_target", "A local review has structured pull request identity.")
	inputPath    = TextField("input_path", "Path to untrusted occurrence data.")
	leafIdentity = Use("delegation.identity", "content/delegation/identity.md")
	seedContract = Use("delegation.seed", "content/delegation/seed.md", Bind("seed_id", Input(seedID)), Bind("seed_title", Input(seedTitle)),
		Bind("completion", Choose(Present(checkIDs),
			Use("delegation.seed-checks", "content/delegation/seed-checks.md", Bind("check_ids", Input(checkIDs))),
			Use("delegation.seed-no-checks", "content/delegation/seed-no-checks.md"))))
	authorization       = TextField("authorization", "The user's words for the task, stored on the seed.")
	authorizationSetter = TextField("set_by", "Name of the session or crew member that set the authorization; blank when the user set it in the app.")
	authorizationLine   = Choose(Present(authorizationSetter),
		Use("delegation.authorization", "content/delegation/authorization.md", Bind("set_by", Input(authorizationSetter)), Bind("authorization", Input(authorization))),
		Use("delegation.authorization-from-user", "content/delegation/authorization-from-user.md", Bind("authorization", Input(authorization))))
	previousAuthorization  = TextField("previous", "The words the change replaces or removes.")
	authorizationChangedBy = When(Present(authorizationSetter),
		Use("delegation.authorization-set-by", "content/delegation/authorization-set-by.md", Bind("set_by", Input(authorizationSetter))))
	authorizationUpdate = Use("delegation.authorization-update", "content/delegation/authorization-update.md",
		Bind("seed_id", Input(seedID)), Bind("seed_title", Quoted(seedTitle)), Bind("set_by", authorizationChangedBy),
		Bind("authorization", Quoted(authorization)),
		Bind("replaces", When(Present(previousAuthorization),
			Exact(Use("delegation.authorization-replaces", "content/delegation/authorization-replaces.md", Bind("previous", Quoted(previousAuthorization)))))))
	authorizationCleared = Use("delegation.authorization-cleared", "content/delegation/authorization-cleared.md",
		Bind("seed_id", Input(seedID)), Bind("seed_title", Quoted(seedTitle)), Bind("set_by", authorizationChangedBy),
		Bind("previous", Quoted(previousAuthorization)))
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
			On("authorization", "message_fragment", "The seed's authorization, after the role block of a delegation or handover opening.", authorizationLine),
			On("identity", "message_fragment", "Delegation identity shared by delegation and automation.", leafOpening(Input(brief))),
			On("authorization-update", "inbox_content", "A change to a seed's authorization by someone other than its tender, typed into the tender's conversation as the user's message.", authorizationUpdate),
			On("authorization-cleared", "inbox_content", "The removal of a seed's authorization by someone other than its tender, typed into the tender's conversation as the user's message.", authorizationCleared),
		}},
		{ID: "automation", Description: "Scheduled or event-triggered visible session.", Events: []Event{
			On("opening", "opening_message", "Configured task, local-review restriction, stake and occurrence input.",
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
