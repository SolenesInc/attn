package prompts

func piRecipient() Recipient {
	environment := ProducedBy(TextField("environment", "Environment rendered from configured slots and notes by Pi."), "pi-environment/render")
	action := func(stage string) Node {
		return Use("pi.action", "content/pi/action.md",
			Bind("cwd", Input(TextField("cwd", "Working directory."))),
			Bind("conversation", Choose(Enabled(FlagField("has_conversation", "The transcript contains at least one entry.")),
				Input(TextField("conversation", "Rendered transcript.")), Use("pi.empty-transcript", "content/pi/empty-transcript.md"))),
			Bind("action", Input(TextField("action", "JSON object containing tool name and action."))),
			Bind("reason", Input(TextField("reason", "Why the deterministic path could not decide."))),
			Bind("stage", Use("pi."+stage, "content/pi/"+stage+".md")))
	}
	return Recipient{ID: "pi-permission", Description: "Separate permission classifier. Both passes receive the same system rulebook; the intent pass runs only when harm exceeds the existing threshold.", Events: []Event{
		On("unreadable", "message_fragment", "Diagnostic supplied as a denial reason.", template("pi.unreadable", "content/pi/unreadable.md", TextField("excerpt", "Bounded classifier output."))),
		On("nothing", "message_fragment", "An empty classifier response.", Use("pi.nothing", "content/pi/nothing.md")),
		On("system", "system_prompt", "Shared by both permission passes.", Exact(template("pi.rulebook", "content/pi/rulebook.md", environment))),
		On("system-with-cache-grants", "system_prompt", "Both classifier passes receive the rulebook and current executor cache grants.", Join("\n\n",
			Input(ProducedBy(TextField("rulebook", "Rulebook with the rendered environment."), "pi-permission/system")),
			template("pi-security.cache-grants", "content/pi/security/cache-grants.md", TextField("paths", "JSON array of current executor build-cache grants.")))),
		On("grant", "user_message", "Opening-message authorization context, when supplied.", template("pi.grant", "content/pi/grant.md", TextField("opening_message", "The session's opening message."))),
		On("harm", "user_message", "First pass: classify harm without user intent.", action("harm")),
		On("intent", "user_message", "Second pass: apply authorization and exceptions.", action("intent")),
	}}
}

func piSessionRecipient() Recipient {
	return Recipient{ID: "pi-session", Description: "The working Pi agent, receiving tool permission results.", Events: []Event{
		On("not-stated", "message_fragment", "An empty action or reason in a denial.", Use("pi.not-stated", "content/pi/not-stated.md")),
		On("denial", "tool_result", "Auto-mode denial, with distinct outage and hard-block guidance.",
			Use("pi.denial", "content/pi/denial.md",
				Bind("action", Input(TextField("action", "Action collapsed to one line."))),
				Bind("reason", Input(TextField("reason", "Reason collapsed to one line."))),
				Bind("outage", When(Enabled(FlagField("outage", "No classifier answered.")), Use("pi.denial-outage", "content/pi/denial-outage.md"))),
				Bind("guidance", Use("pi.denial-guidance", "content/pi/denial-guidance.md")),
				Bind("settings", Choose(Enabled(FlagField("hard_block", "User approval cannot clear this rejection.")),
					Use("pi.denial-hard-block", "content/pi/denial-hard-block.md"),
					Use("pi.denial-settings", "content/pi/denial-settings.md"))),
			)),
	}}
}

func piEnvironmentRecipient() Recipient {
	recipient := Recipient{ID: "pi-environment", Description: "Environment supplied to both permission passes, including the rulebook's defaults."}
	for _, name := range []string{"trusted_repo", "repo_visibility", "domains", "buckets", "services", "source_control", "registry", "sensitive_data", "audiences", "remote_targets", "iac_scopes"} {
		recipient.Events = append(recipient.Events, On("unset-"+name, "message_fragment", "Default meaning when the user has not configured this slot.", Use("pi-environment.unset-"+name, "content/pi/environment/"+name+".md")))
	}
	recipient.Events = append(recipient.Events,
		On("render", "message_fragment", "Ordered environment slots and optional user notes.", Join("", Input(ProducedBy(TextField("slots", "Rendered slots joined with newlines."), "pi-environment/slot")),
			When(Enabled(FlagField("has_notes", "At least one nonblank note.")), Join("\n\n", Compose(), template("pi-environment.notes", "content/pi/environment/notes.md", TextField("notes", "Quoted note lines.")))))),
		On("slot", "message_fragment", "One configured environment slot, or its default meaning.", template("pi-environment.slot", "content/pi/environment/slot.md", TextField("label", "Schema label."), TextField("value", "Configured values or the slot's rendered default."))),
		On("notes", "message_fragment", "Nonempty notes supplied by the user.", template("pi-environment.notes", "content/pi/environment/notes.md", TextField("notes", "Quoted note lines."))),
	)
	return recipient
}
