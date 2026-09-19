package prompts

func piGuardianRecipient() Recipient {
	source := func(name string, bindings ...Binding) Node {
		return Use("pi-guardian."+name, "content/pi/guardian/"+name+".md", bindings...)
	}
	environment := ProducedBy(TextField("environment", "Environment rendered from configured slots and notes by Pi."), "pi-environment/render")
	policy := source("policy", Bind("environment", Input(environment)))
	system := Join("\n\n", source("policy-template", Bind("policy", policy)), Exact(source("output-contract")))

	return Recipient{ID: "pi-guardian", Description: "Guardian reviewer for auto mode. It judges one planned action against the security policy and answers allow or deny.", Events: []Event{
		On("system", "system_prompt", "The guardian's whole system prompt; the security policy carries the configured environment.", system),
	}}
}
