package prompts

func crewRecipient() Recipient {
	name := TextField("display_name", "Display name")
	home := TextField("home_dir", "Home dir")
	charter := FlagField("has_charter", "A charter exists")
	cwd := TextField("cwd", "Cwd")
	awareness := TextField("awareness_dirs", "Awareness dirs")
	handoff := TextField("handoff", "Handoff")
	older := TextField("older_handoffs", "Older handoffs")
	handoffsDirname := TextField("handoffs_dirname", "Handoffs dirname")
	holdings := ProducedBy(Trimmed(TextField("garden_holdings", "Rendered garden holdings, supplied by internal/crew.")), "crew/garden")

	heldSeeds := TextField("held_seeds", "Held seeds with their freshest handoff notes, one entry per blank-line-separated block.")
	heldTotal := TextField("held_total", "How many seeds the member holds; blank unless the list was cut.")
	heldLimit := TextField("held_limit", "How many held seeds this block lists.")
	plotReady := TextField("plot_ready", "One line per plot with its ready count.")
	gardenRead := FlagField("garden_read", "The garden was readable when the member woke.")

	identity := template("crew.identity", "content/crew/identity.md", name, home)
	existingCharter := template("crew.charter", "content/crew/charter.md", TextField("charter_file", "Charter file"))
	firstDay := template("crew.first-day", "content/crew/first-day.md", TextField("charter_path", "Charter path"))
	launchDirectory := template("crew.cwd", "content/crew/cwd.md", cwd)
	awarenessDirectories := template("crew.awareness", "content/crew/awareness.md", awareness)
	letter := template("crew.letter", "content/crew/letter.md", TextField("handoff_name", "Handoff name"))
	olderLetters := template("crew.older-letters", "content/crew/older-letters.md", handoffsDirname, older)
	noLetter := template("crew.no-letter", "content/crew/no-letter.md", TextField("handoffs_dir", "Handoffs dir"))
	closure := template("crew.closure", "content/crew/closure.md", handoffsDirname, name)

	heldList := template("crew.garden", "content/crew/garden.md", heldSeeds)
	heldMore := template("crew.garden-more", "content/crew/garden-more.md", heldTotal, heldLimit)
	plots := template("crew.garden-plots", "content/crew/garden-plots.md", plotReady)
	noHoldings := Use("crew.garden-empty", "content/crew/garden-empty.md")

	return Recipient{ID: "crew", Description: "Permanent crew member: launch identity and lifecycle messages.", Events: []Event{
		On("truncated-handoff", "message_fragment", "The adapter cuts the letter at a UTF-8 boundary; the catalog adds its full-file location.", template("crew.truncated-handoff", "content/crew/truncated-handoff.md", TextField("text", "Bounded letter."), TextField("limit", "Filing limit in bytes."), TextField("total", "Original bytes."), TextField("path", "Full letter path."))),
		On("trimmed-note", "message_fragment", "The adapter cuts a seed's handoff note at a UTF-8 boundary; the catalog adds the command for the whole note.", template("crew.trimmed-note", "content/crew/trimmed-note.md", TextField("text", "Bounded note."), TextField("limit", "Priming budget in bytes."), TextField("total", "Original bytes."), TextField("seed", "Seed the note belongs to."))),
		On("garden", "message_fragment", "internal/crew.Priming.GardenSection: the seeds the member holds. The adapter reads the garden, applies the held-seed tripwire and bounds each handoff note.",
			When(Enabled(gardenRead),
				Choose(Present(heldSeeds),
					Compose(heldList, When(Present(heldTotal), heldMore), When(Present(plotReady), plots)),
					noHoldings))),
		On("priming", "launch_instructions", "internal/crew.Priming.Block: appended to launch instructions. The adapter prepares paths, display name and bounded handoff text.",
			Compose(
				Join("", identity,
					Choose(Enabled(charter), existingCharter, firstDay),
					When(Present(cwd), launchDirectory),
					When(Present(awareness), awarenessDirectories)),
				Choose(Present(handoff),
					Compose(Join("", letter, When(Present(older), olderLetters)), Input(handoff)),
					noLetter),
				When(Present(holdings), Input(holdings)),
				closure)),
		On("wake", "user_message", "crew_wake.go: today's first message.", Use("crew.wake", "content/crew/wake.md")),
		On("successor", "user_message", "crew_handoff.go: successor after a nap.", Use("crew.successor", "content/crew/successor.md")),
		On("sleep-requested", "user_message", "crew_sleep.go: user requested closure.", Use("crew.sleep-requested", "content/crew/sleep-requested.md")),
		On("heartbeat", "user_message", "crew_lifecycle.go: keep the context cache warm.", Use("crew.heartbeat", "content/crew/heartbeat.md")),
		On("sleep-away", "user_message", "crew_lifecycle.go: close after the user has been away.", Use("crew.sleep-away", "content/crew/sleep-away.md")),
	}}
}
