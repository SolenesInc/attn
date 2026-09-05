package prompts

func piSecurityRecipient() Recipient {
	text := func(name string, bindings ...Binding) Node {
		return Use("pi-security."+name, "content/pi/security/"+name+".md", bindings...)
	}
	field := func(name, description string) Node { return Input(TextField(name, description)) }
	review := Enabled(FlagField("review_available", "Auto-mode access review is available."))
	network := Enabled(FlagField("network_failure", "The command reported a network failure."))
	writeRecovery := text("write-recovery",
		Bind("paths", field("write_paths", "Writable paths joined with commas.")),
		Bind("guidance", Choose(review, text("write-request"), text("review-unavailable"))))
	sandboxGuidance := text("enabled",
		Bind("paths", field("write_paths", "JSON array of writable paths.")),
		Bind("caches", field("cache_paths", "JSON array of cache grants, or disabled.")),
		Bind("unavailable", When(Enabled(FlagField("has_unavailable_caches", "Some cache grants are unavailable.")),
			Exact(text("unavailable-caches", Bind("paths", field("unavailable_caches", "JSON array of unavailable cache grants.")))))),
		Bind("review", field("review", "Available or unavailable, derived from reviewer configuration.")),
		Bind("request", Choose(review, text("access-request"), text("review-unavailable"))),
		Bind("local_cache", text("local-cache")),
		Bind("refusal", text("refusal")))
	instructions := text("instructions",
		Bind("sandbox", field("sandbox", "Enabled or disabled, derived from policy.")),
		Bind("network", field("network", "Effective tool networking policy.")),
		Bind("guidance", Choose(Enabled(FlagField("enabled", "The OS sandbox is enabled.")), sandboxGuidance, text("disabled"))),
		Bind("credentials", text("credentials")))

	networkRecovery := Choose(review, text("network-request"), text("review-unavailable"))
	writeFailureNetworkAdvice := Choose(network, Compose(),
		When(Enabled(FlagField("network_denied", "Tool networking is denied.")),
			When(review, Join("\n", Compose(), text("network-request")))))
	recovery := Join("\n",
		Choose(network, text("network-failure"), text("permission-failure")),
		Join("", Choose(network, networkRecovery, writeRecovery), writeFailureNetworkAdvice),
		text("execution-scope"))

	return Recipient{ID: "pi-security", Description: "Execution permissions supplied to the working Pi agent and sandbox tool guidance.", Events: []Event{
		On("instructions", "system_prompt", "Appended before each agent start; values come from the executor's current policy.", instructions),
		On("write-recovery", "tool_result", "Guidance after a sandbox write failure.", writeRecovery),
		On("recovery", "tool_result", "Appended to failed commands; distinguishes OS errors from review refusals.", recovery),
		On("denial", "tool_result", "An access request refused by auto mode.", text("denial",
			Bind("action", field("action", "Action collapsed to one line.")), Bind("reason", field("reason", "Reason collapsed to one line.")),
			Bind("outage", When(Enabled(FlagField("outage", "No classifier answered.")), Exact(text("denial-outage")))),
			Bind("guidance", Choose(Enabled(FlagField("hard_block", "User approval cannot clear this rejection.")), text("denial-hard-block"), text("denial-retry"))),
			Bind("boundary", text("denial-boundary")))),
		On("review-unavailable", "tool_result", "No access reviewer is available.", text("review-unavailable")),
		On("startup-failure", "tool_result", "The OS sandbox could not start.", text("startup-failure")),
		On("configuration-error", "system_prompt", "Security configuration prevents tool execution.", text("configuration-error", Bind("problem", field("problem", "Security configuration error.")))),
		On("not-initialized", "system_prompt", "Security has not initialized.", text("not-initialized")),
		On("bash-description", "tool_description", "Appended to the harness's bash description.", text("bash-description")),
		On("bash-guidance", "tool_guideline", "Sandbox access guidance for bash.", text("bash-guidance")),
		On("bash-credentials", "tool_guideline", "Credential restrictions for bash.", text("bash-credentials")),
	}}
}
