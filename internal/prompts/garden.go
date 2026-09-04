package prompts

func gardenReadyEvent() Event {
	hasCrown := FlagField("has_crown", "This session reports to a seed.")
	hasReady := FlagField("has_ready", "The ready list is nonempty.")
	oneReady := FlagField("one_ready", "Exactly one seed is ready.")
	isPlot := FlagField("is_plot", "The reporting seed is a plot.")
	title := TextField("title", "Quoted reporting seed title.")
	return On("garden-ready", "hook_context", "Garden readiness appended by session-start hooks and attn seed prime.",
		Use("session.garden-ready", "content/session/garden-ready.md", Bind("body",
			Choose(Enabled(hasCrown),
				Choose(Enabled(isPlot),
					Choose(Enabled(hasReady),
						template("session.plot-ready", "content/session/plot-ready.md", seedID, title, ProducedBy(TextField("rows", "Ready seeds and handoff rows, including their leading newlines."), "session/garden-row")),
						template("session.plot-blocked", "content/session/plot-blocked.md", seedID)),
					template("session.dispatched-seed", "content/session/dispatched-seed.md", seedID, title)),
				Choose(Enabled(hasReady),
					Choose(Enabled(oneReady), Use("session.ready-one", "content/session/ready-one.md"),
						template("session.ready-many", "content/session/ready-many.md", TextField("count", "Number of ready seeds."))),
					Use("session.ready-none", "content/session/ready-none.md"))),
		)))
}

func gardenRowEvent() Event {
	author := TextField("author", "Handoff author, when known.")
	handoff := Trimmed(TextField("handoff", "Latest handoff body."))
	return On("garden-row", "message_fragment", "A ready seed and its latest handoff.", Use("session.garden-row", "content/session/garden-row.md",
		Bind("seed_id", Input(seedID)), Bind("slug", Input(TextField("slug", "Seed slug."))), Bind("title", Input(TextField("title", "Seed title."))),
		Bind("handoff", When(Present(handoff), Use("session.garden-handoff", "content/session/garden-handoff.md", Bind("body", Input(handoff)), Bind("author", Choose(Present(author), Input(author), Use("session.previous-tender", "content/session/previous-tender.md")))))),
	))
}
