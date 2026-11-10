package prompts

func codeNavigationRecipient() Recipient {
	text := func(name, description string) Field { return TextField(name, description) }
	reason := text("reason", "Why the language has no working server: the program missing, or the failed server's error.")
	brief := Use("code-navigation.setup", "content/code-navigation/setup.md",
		Bind("language", Input(text("language", "The language with no working server, as attn names it, such as go."))),
		Bind("repository", Input(text("repository", "The review's repository, absolute on the agent's machine."))),
		Bind("document", Input(text("document", "The review document, absolute."))),
		Bind("file", Input(text("file", "The review's first file in the language, relative to the repository."))),
		Bind("failure", Choose(Enabled(FlagField("failed", "A server started and failed; otherwise none was found.")),
			template("code-navigation.failed", "content/code-navigation/failed.md", reason),
			template("code-navigation.missing", "content/code-navigation/missing.md", reason))))
	return Recipient{ID: "code-navigation", Description: "Code navigation in a shown review.", Events: []Event{
		On("setup", "message_fragment", "The brief of the agent the review reader's setup key starts on the review's machine, delegated as a new assignment.", brief),
	}}
}
