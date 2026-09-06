package prompts

func piSecurityRecipient() Recipient {
	text := func(name string, bindings ...Binding) Node {
		return Use("pi-security."+name, "content/pi/security/"+name+".md", bindings...)
	}
	field := func(name, description string) Node { return Input(TextField(name, description)) }
	network := Enabled(FlagField("network_failure", "The command reported a network failure."))
	writeRecovery := text("write-recovery",
		Bind("paths", field("write_paths", "Writable paths joined with commas.")),
		Bind("guidance", text("sandbox-limits")))
	sandboxGuidance := text("enabled",
		Bind("paths", field("write_paths", "JSON array of writable paths.")),
		Bind("caches", field("cache_paths", "JSON array of cache grants, or disabled.")),
		Bind("unavailable", When(Enabled(FlagField("has_unavailable_caches", "Some cache grants are unavailable.")),
			Exact(text("unavailable-caches", Bind("paths", field("unavailable_caches", "JSON array of unavailable cache grants.")))))),
		Bind("request", text("sandbox-limits")),
		Bind("local_cache", text("local-cache")),
		Bind("refusal", text("refusal")))
	instructions := text("instructions",
		Bind("sandbox", field("sandbox", "Enabled or disabled, derived from policy.")),
		Bind("network", field("network", "Effective tool networking policy.")),
		Bind("guidance", Choose(Enabled(FlagField("enabled", "The OS sandbox is enabled.")), sandboxGuidance, text("disabled"))),
		Bind("credentials", text("credentials")))

	recovery := Join("\n",
		Choose(network, text("network-failure"), text("permission-failure")),
		text("sandbox-limits"),
		text("execution-scope"))

	return Recipient{ID: "pi-security", Description: "Execution permissions supplied to the working Pi agent and sandbox tool guidance.", Events: []Event{
		On("instructions", "system_prompt", "Appended before each agent start; values come from the executor's current policy.", instructions),
		On("write-recovery", "tool_result", "Guidance after a sandbox write failure.", writeRecovery),
		On("recovery", "tool_result", "Appended to failed commands; distinguishes OS errors from approval rejections.", recovery),
		On("startup-failure", "tool_result", "The OS sandbox could not start.", text("startup-failure")),
		On("configuration-error", "system_prompt", "Security configuration prevents tool execution.", text("configuration-error", Bind("problem", field("problem", "Security configuration error.")))),
		On("not-initialized", "system_prompt", "Security has not initialized.", text("not-initialized")),
		On("bash-credentials", "tool_guideline", "Credential restrictions for bash.", text("bash-credentials")),
	}}
}
