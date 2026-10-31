package prompts

func crewRecipient() Recipient {
	name := TextField("display_name", "Display name")
	v2 := FlagField("crew_v2_enabled", "Use the crew v2 operating model.")
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
	chief := FlagField("chief", "The member is its profile's Chief.")
	chiefNotebook := TextField("notebook_root", "The Chief's profile Notebook directory.")
	previousChief := TextField("previous_chief", "On the Chief's first day, the session that was the profile's Chief before Crew v2.")
	chiefGuidance := When(Enabled(chief), Compose(
		template("crew.v2.chief", "content/crew/v2/chief.md", chiefNotebook),
		When(Present(previousChief), template("crew.v2.chief-switchover", "content/crew/v2/chief-switchover.md", previousChief))))

	identity := Use("crew.identity", "content/crew/identity.md", Bind("display_name", Input(name)), Bind("home_dir", Input(home)),
		Bind("home_ownership", Use("crew.home-files", "content/crew/home-files.md")))
	v2Identity := template("crew.v2.identity", "content/crew/v2/identity.md", name, home)
	charterFile := TextField("charter_file", "Charter file")
	existingCharter := template("crew.charter", "content/crew/charter.md", charterFile)
	charterPath := TextField("charter_path", "Charter path")
	v2Charter := template("crew.v2.charter", "content/crew/v2/charter.md", charterPath)
	firstDay := template("crew.first-day", "content/crew/first-day.md", charterPath)
	v2FirstDay := template("crew.v2.first-day", "content/crew/v2/first-day.md", charterPath)
	launchDirectory := template("crew.cwd", "content/crew/cwd.md", cwd)
	awarenessDirectories := template("crew.awareness", "content/crew/awareness.md", awareness)
	launchContext := Compose(
		Choose(Enabled(v2),
			Join("", v2Identity, Choose(Enabled(charter), v2Charter, v2FirstDay), When(Present(cwd), launchDirectory), When(Present(awareness), awarenessDirectories)),
			Join("", identity, Choose(Enabled(charter), existingCharter, firstDay), When(Present(cwd), launchDirectory), When(Present(awareness), awarenessDirectories))),
		When(Enabled(v2), Use("crew.v2.compaction", "content/crew/v2/compaction.md")))
	handoffName := TextField("handoff_name", "Handoff name")
	letter := Choose(Enabled(v2),
		template("crew.v2.letter", "content/crew/v2/letter.md", handoffName),
		template("crew.letter", "content/crew/letter.md", handoffName))
	olderLetters := Choose(Enabled(v2),
		template("crew.v2.older-letters", "content/crew/v2/older-letters.md", handoffsDirname),
		template("crew.older-letters", "content/crew/older-letters.md", handoffsDirname, older))
	handoffsDir := TextField("handoffs_dir", "Handoffs dir")
	noLetter := Choose(Enabled(v2),
		template("crew.v2.no-letter", "content/crew/v2/no-letter.md", handoffsDir),
		template("crew.no-letter", "content/crew/no-letter.md", handoffsDir))
	closure := Choose(Enabled(v2),
		template("crew.v2.closure", "content/crew/v2/closure.md", handoffsDirname),
		template("crew.closure", "content/crew/closure.md", handoffsDirname, name))

	heldList := Choose(Enabled(v2),
		template("crew.v2.garden", "content/crew/v2/garden.md", heldSeeds),
		template("crew.garden", "content/crew/garden.md", heldSeeds))
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
				launchContext,
				When(Enabled(v2), Use("crew.v2.operating-model", "content/crew/v2/operating-model.md")),
				When(Enabled(v2), chiefGuidance),
				Choose(Present(handoff),
					Compose(Join("", letter, When(Present(older), olderLetters)), Input(handoff)),
					noLetter),
				When(Present(holdings), Input(holdings)),
				closure)),
		On("wake", "user_message", "crew_wake.go: today's first message.", Choose(Enabled(v2), Use("crew.v2.wake", "content/crew/v2/wake.md"), Use("crew.wake", "content/crew/wake.md"))),
		On("successor", "user_message", "crew_handoff.go: successor on a new harness after crew apply.", Choose(Enabled(v2), Use("crew.v2.successor", "content/crew/v2/successor.md"), Use("crew.successor", "content/crew/successor.md"))),
		On("sleep-requested", "user_message", "crew_sleep.go: user requested closure.", Use("crew.sleep-requested", "content/crew/sleep-requested.md")),
		On("settings-applied", "user_message", "crew_apply.go: the member's session reloaded in place with its saved settings.", Use("crew.settings-applied", "content/crew/settings-applied.md", Bind("charter_path", Input(TextField("charter_path", "Charter path."))))),
		On("switch-requested", "user_message", "crew_apply.go: the member's saved harness differs from its running session.", Use("crew.switch-requested", "content/crew/switch-requested.md", Bind("agent", Input(TextField("agent", "Saved harness."))))),
		On("heartbeat", "user_message", "crew_lifecycle.go: keep the context cache warm.", Use("crew.heartbeat", "content/crew/heartbeat.md")),
		On("sleep-away", "user_message", "crew_lifecycle.go: close after the user has been away.", Use("crew.sleep-away", "content/crew/sleep-away.md")),
	}}
}
