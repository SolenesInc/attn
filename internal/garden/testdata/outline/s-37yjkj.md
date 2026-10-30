Make closing a seed unambiguous: who may close a seed another session tends, what a repeated or conditional close does, and what harvest does to a plot that still has open children. Today orchestrators force-close their own builders' seeds after merging (82 forced moves), a second harvest fails and points to replant, `--when-merged` throws away the reason agents give it, and plan seeds that became plots get harvested over open work.

The one reason behind every decision: a close succeeds when the closer has standing and the end state is right, and otherwise refuses before anything moves, on one line that names the next command.

Done when an orchestrator closes its delegate's seed without --force, a second harvest exits 0, `--when-merged -m` keeps the reason, a plot with open children refuses harvest and wither before moving, closing a plot's last open child tells the closer and rings the plot's tender, and an agent tending a plot hears its children's moves without a watch.

Out of scope: closing a session and knowing its work is saved (close-session-knowing-work-saved, s-evwt1k, split out by the user); plot views and bell text (read-only-what-changed-seed-plot, s-6chqf8); check ids (checks-without-coordination-copies, s-m3c7ve).

## Terms

- standing: the right to move a seed that another live session tends, without --force. The actor is above the tender in its delegation chain, or is the profile's chief. It is the rule `attn agent close` already uses.
- open child: a seed whose part-of edge points at the plot and whose state is planted, growing or dormant.
- converge: a move to the state the seed is already in succeeds and changes nothing, except that a new reason is kept as a note.
- covering tender: the tender of a seed's plot, or of any plot above it.

## Decisions

- D1. Standing follows the agent close rule. A session above the tender in its delegation chain, or the profile's chief, makes any of the five moves without --force. Everyone else still needs --force. A crew member's named claim yields only to the chief. An actor with no session (the app, a shell outside attn) keeps today's behavior; the app board already passes force whenever another session holds the seed.
- D2. A move made by standing is recorded on the log as forced moves are today: who moved the seed, how, and whose claim ended. The delegate hears it through the watch its delegation created.
- D3. Moves converge. Tend, park, harvest, wither and replant to the state the seed is already in exit 0. A reason that differs from the stored one lands as a note. Harvest on a withered seed and wither on a harvested one still refuse, naming replant. Arming `--when-merged` on a harvested seed converges too.
- D4. `harvest --when-merged -m "<reason>"` keeps the reason on the harvest condition, and the merge harvests with it. Without -m the merge writes today's "PR #N merged: <title>". The arming line says the command merges nothing.
- D5. Harvest and wither refuse a seed with open children, before the move, for every actor and with --force. The one-line refusal names every open child and the ways out: `replant` hands the plot back when your part is done; close the open seeds first; or move follow-ups out of the plot. The warning printed after the move goes.
- D6. Closing a plot's last open child appends a note on the plot that rings its tender and watchers, and the move's output names the plot. No new bus event: a ringing note already reaches the plot's parties and stays on its log.
- D7. Every lifecycle refusal is one line: the cause, then the next command. Every move prints who it rang, the way `note --ring` does.

- D8. Covering tenders are seed parties. A session or crew member tending a plot hears every seed update from the seeds under it, for as long as it holds the claim, with no watch row. This changes who existing events reach; the user approved it.

Open: none.

## Tradeoffs accepted

- D1. We accept that a dispatcher can close a seed its live delegate is still working on, in exchange for no --force on the routine handoff. The log names who closed it, and the delegate is rung.
- D3. We accept that a mistaken repeated harvest only prints the earlier reason, in exchange for retries and racing closers ending in the same state.
- D5. We accept one more command for an agent that meant "the rest are follow-ups", in exchange for never closing a plot over open work. --force does not bypass it, because the refusal is about the work's state, not about a claim.
- D6. We accept one note on a plot's log each time it runs out of open seeds, in exchange for no new event. A plot whose next step is planted later keeps an "all closed" note that is no longer true.
- D8. We accept more seed updates for an agent that tends a plot by hand, in exchange for no stored watch that outlives the claim. An agent delegated onto a plot already watched it through its delegation, so it hears nothing new. A plot's tender hears its last child's close twice: the child's move and the plot note from D6.
- Closed plots that already hold open children (4 in the 2026-10-28 copy of the garden) stay as they are. Nothing reopens them.

## Findings

Code facts are at origin/main bdf3b487c. Counts come from the study on make-garden-easy-agents-read-write (s-bhb3t4, findings-lifecycle.md) and the deeper pass attached here (closing-study.md). M is measured, E is estimated from hand labels.

- F1. Forced closes are routine handoffs. 89 forced moves (81 harvest, 6 wither, 2 replant) in 19 sessions; 82 were the dispatcher's (M). In about 60 of those 82 the builder had said "ready to merge" and waited for the dispatcher to merge; 7 waited on the user's merge; 8 had reported done without harvesting (E; only 27 have a delegate transcript). The dispatcher also ran `attn agent close` on the delegate within two hours in at least 61 (M). Requires standing for the dispatcher (D1). Rules out lifting the tender check only on harvest: dispatchers also wither and replant.
- F2. Two authority rules exist. `attn agent close` lets a session act on itself and on anyone below it in its delegation chain, and the chief on any agent (`mayActOnSession`, internal/daemon/session_authority.go). Seed moves let only the live tender act (`garden.Transition`, `refuseTakeover` in internal/garden/lifecycle.go). `mayActOnSession` passes an empty caller, so standing must require a named actor session; otherwise the app and shells outside attn would override every claim.
- F3. The races are the dispatcher's. 8 forced harvests failed with "already harvested; `attn seed replant` is the way out" because the delegate or its `--when-merged` got there first; no delegate was seen hitting it after a forced close (M). `refuseState` sends a same-state move to replant, the wrong exit. Requires D3.
- F4. `--when-merged -m`. 23 of 112 armings passed -m and all were refused, by `harvestWhenArgs` in cmd/attn/seed.go and again by `applyHarvestWhenRequest` in the daemon. The -m held an outcome and a PR number 23/23 and verification 18/23; 13 agents re-ran without -m and lost the text (M). Arming already releases the claim by parking the seed (`armHarvestWhenMerged`), so a builder that arms it never collides with its dispatcher. The merge reason today is `harvestWhenMergedReason`. Claude's auto-mode classifier refused `harvest --when-merged` as a merge in 2 sessions.
- F5. Plot harvests. 11 sessions harvested a plot with open children and saw the warning only after the move (`fprintTransition`/`openPlotSeeds`, CLI only). 4 Pathfinders meant "my plan is done". 7 moved the children out (unlink part-of, link discovered-from). 2 piped to `tail -1`, saw only the warning, retried and hit "already harvested" (M). Live garden on 2026-10-30 (newest 1000 of 2152 seeds): 5 open plots with every child closed, 2 to 24 days after the last close. In the 2026-10-28 copy, 20 of 89 finished plots stayed open more than a day, 10 of them still open (median 28 days). Requires D5 before the move and D6 after the last close. Rules out closing a plot by itself when its last child closes: make-garden-easy-agents-read-write had every child closed between its steps 2 and 3, before step 3 was planted.
- F6. Who hears a move. Lifecycle bells go to the seed's current tender and the watchers covering it, except the session that caused the move (`seedParties` in `events.Declarations`, internal/garden/events/catalog.go). A delegation creates a watch for the delegate on its own seed (`bindDelegationAssignmentProtected`, delegate_seed.go), so the delegate is rung when its dispatcher closes the seed; the dispatcher gets no watch. A tender whose claim a move ends stops being a party, so a hand-tended seed's tender is not rung when someone else closes it. About 26% of harvests were followed by a manual `agent msg` or ring, and a move does not say who heard it. `seedParties` in garden_checks.go already previews the audience of a note `--ring`.
- F7. Transitions run under `lockGardenRoles` in `applySeedTransitionDetailedAsAtRevisionProtected`, and closing moves already read the whole garden there to compute `Unblocks`. The open-children check and the emptied-plot check belong in that read, so two children closed at once cannot both miss or both announce the plot. That read is skipped today when `ask.SuppressNotification` is set (automations); the plot rule must run regardless.
- F8. Other callers reach the same transition: the app board (`handleSeedTransitionWS`; GardenBoard.tsx passes force when another session holds the seed), automations (`automations_deliver.go` closes, replants and tends), vents (`vents.go` withers an unplanted vent seed), and harvest-when fulfilment (daemon actor, force). Each gets D3, D5 and D6 through the shared `Transition`. Automations must not depend on "already" errors.
- F9. Agents cut lifecycle output with tail or head (tend 46%, replant 60%). `refuseTakeover` puts the cause on line 1 and "Pass --force…" on line 2, so many agents see only the --force line.

## Design

Ownership:

    internal/garden/lifecycle.go      Transition: standing (D1), converge (D3), open-children refusal (D5), one-line refusals (D7)
    internal/garden/plot.go           OpenChildren(seeds, id) (D5, D6)
    internal/garden/garden.go         HarvestCondition.Reason (D4)
    internal/garden/events/catalog.go seedParties includes CoveringTenders (D8)
    internal/daemon/garden_events.go  ResolveSeedRole resolves CoveringTenders by walking ancestors' tenders (D8)
    internal/daemon/garden.go         Standing per attempt; audit note; plot note; ring_recipients, noted, plot_emptied
    internal/daemon/session_authority.go  mayActOnSession, reused unchanged
    internal/daemon/garden_harvest_when.go  arming keeps -m; fulfilment uses it; converges on harvested
    internal/protocol/schema/main.tsp SeedTransitionResult and SeedHarvestCondition fields
    cmd/attn/seed.go                  accepts -m with --when-merged; prints rang, noted and plot lines; deletes openPlotSeeds
    internal/prompts/content/garden.md and the attn skill (garden.md, delegated-agent.md, delegation.md)  describe the moves

Interfaces. The call sites first:

    # The orchestrator merged its builder's PR; the builder is still live and tending.
    $ attn seed harvest s-a2af6y -m "PR #776 merged; C3 confirmed"
    s-a2af6y is harvested — PR #776 merged; C3 confirmed · ended 938473fe's claim (in your delegation chain) · rang 938473fe

    # The merge already harvested it.
    $ attn seed harvest s-a2af6y -m "merged; checks confirmed"
    s-a2af6y was already harvested at 11:25 — PR #776 merged: Fix … · your reason is on its log

    # A builder that will not merge.
    $ attn seed harvest s-a2af6y --when-merged https://github.com/o/r/pull/776 -m "Fix in #776; wire tests pass"
    s-a2af6y harvests itself with your reason when o/r#776 merges; this command merges nothing · claim released

    # A Pathfinder closing the plan it turned into a plot.
    $ attn seed harvest s-37yjkj -m "plan ready"
    seed harvest: s-37yjkj has 3 open seeds under it (s-r0rzfr growing, s-d5fsnn planted, s-a2nvd0 planted); a plot closes after them: `attn seed replant s-37yjkj` hands it back if your part is done, or close or move those seeds first

    # The last open child.
    $ attn seed harvest s-a2nvd0 -m "…"
    s-a2nvd0 is harvested — … · s-37yjkj has no open seeds left and stays open: close it or plan its next step · rang …

    # A session outside the chain.
    seed harvest: s-x is tended by 4f2a (live); only it, sessions above it in its delegation chain, or the profile's chief move it; --force acts anyway and the log records it

The types behind them:

    // internal/garden/lifecycle.go
    type Standing interface {
        Live(protocol.SessionID) bool     // a session's claim lapses when the session ends
        Oversees(actor, held Tender) bool // the agent close rule; false for an actor with no session
    }

    type Outcome struct {
        Seed      Seed
        Moved     bool    // false when the seed already was where the verb leads (D3)
        Displaced *Tender // the live claim this move ended, when it was not the actor's
        By        Means   // MeansTender, MeansStanding, MeansForce
    }

    func Transition(seed Seed, verb Verb, ask Ask, standing Standing, openChildren []Seed) (Outcome, error)

    // internal/garden/garden.go
    type HarvestCondition struct {
        …
        Reason string `json:"reason,omitempty"` // D4; empty on documents written before this change
    }

    // internal/protocol/schema/main.tsp, then make generate-types
    model SeedTransitionResult {
      …
      "ring_recipients"?: string[]; // who this move rang, as on the note result
      "noted"?: SeedNote;           // the reason, kept as a note when the move converged
      "plot_emptied"?: Seed;        // the plot this move left with no open seeds
    }
    model SeedHarvestCondition { …; "reason"?: string; }

`HarvestCondition` lives in the seed's JSON document, so old documents decode with an empty reason and need no migration.

Invariants and what enforces them:

- No path closes a seed with open children. `Transition` is the one place every caller goes through (F8); C65 drives each caller.
- Standing needs a named live actor session. `Oversees` returns false otherwise; C63 covers a caller with no session.
- A converged move writes nothing and publishes nothing. C60 asserts no bell.
- A covering tender stops hearing a plot's seeds when its claim ends. The role is resolved from the current claims at delivery, as `RecipientStillBelongsTo` already does for watchers; C67 covers it.

Behavior, for harvest:

    handleSeedTransition / handleSeedTransitionWS
    └─ applySeedTransitionDetailedAsAtRevisionProtected      lockGardenRoles held
       ├─ readSeed
       ├─ readGarden                                         closing verbs, always (F7)
       ├─ garden.Transition(seed, verb, ask, standing, OpenChildren(read, id))
       │  ├─ already there → Outcome{Moved: false}           D3
       │  ├─ closing with open children → one-line refusal   D5
       │  ├─ another live claim → Oversees: MeansStanding · Force: MeansForce · else one-line refusal   D1
       │  └─ reason rules, as today
       ├─ not moved: reason differs → append it as a note → return noted
       ├─ prepare audit note when Displaced                D2
       ├─ plot has no open children after this move → prepare ringing plot note   D6
       ├─ commit seed, notes and events together; per-note subject, attention and cause
       ├─ return plot_emptied only for that successful transition
       └─ ring_recipients from the move, unblocked seeds and ringing plot note   D7

State: D8 adds none; covering tenders are derived from claims each time a bell is resolved. The only new state is `HarvestCondition.Reason`, owned by the seed document. Arming sets it, fulfilment reads it, and it goes when the condition clears. The plot note is an ordinary note committed atomically with the child close. The existing transaction writer gives each note its own subject, attention flag and cause, so audit/comment notes remain quiet while the plot note rings. A separate append after the close could be lost permanently when it fails, because a retry converges before writing another lifecycle event. A converged move writes nothing, so a retry or a second closer leaves the same end state.

## Execution

The user chose the current trunk workflow in a-vas5c1, replacing the original
three-PR plan. One Builder implements the children in this order, rebases onto
origin/main before each slice, verifies it locally, and lands it through
scripts/land.sh. Review runs on main. No changelog fragment is needed.
Production install is not authorized.

1. closing-moves-converge-say-who-heard (s-r0rzfr): D3, D4, D7.
2. seed-moves-follow-agent-close-rule (s-d5fsnn): D1, D2.
3. plot-closes-after-children (s-a2nvd0): D5, D6, D8.

The user confirmed D6 in a-sk4w3m: keep the ringing note on the plot.
The user also authorized merging, superseded here by direct trunk landing.

Each slice updates the text that describes its behavior. Leave
internal/prompts/content/delegation/seed.md alone unless a line becomes wrong;
sibling seeds s-6chqf8 and s-m3c7ve also change that file.

## Completion

Report C60-C67 on their children and shared C68 for each landed slice. Attach
transcripts and test output to the child that proves them. Harvest a child when
its behavior, verification and delivery are complete; harvest the plot after all
three children are complete. Link the landed commits. Delivery is complete at landing; main's CI and review are not waited on.

## Alternatives rejected

- Anyone may close a seed, dropping the tender check on harvest and wither. Lost to D1: the user chose the rule `attn agent close` already uses, so one rule governs acting on another session's work.
- A plot closes itself when its last child closes. Lost to D5 and D6: it closes plots whose next step is not planted yet (F5).
- `tend` creates a watch, as a delegation does. Lost to D8: the watch row outlives the claim.
- Leave plot updates to read-only-what-changed-seed-plot (s-6chqf8). Lost to D8: the user asked for it here; that seed's plot view still covers reading a plot as a whole.
- A new bus event for "plot emptied". Lost to D6: a ringing note reaches the same parties, needs no new event, and stays on the plot's log.
- Keep the tender check and teach builders `--when-merged`. Lost to D1: 15 of 82 forced closes had no merge pending (E), and dispatchers also wither and replant.
