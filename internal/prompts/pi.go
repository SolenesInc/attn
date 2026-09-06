package prompts

func piEnvironmentRecipient() Recipient {
	recipient := Recipient{ID: "pi-environment", Description: "Environment the user configured for a workspace, rendered into the Guardian's security policy."}
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
