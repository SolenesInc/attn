package main

import (
	"fmt"
	"io"
	"os"
)

// `attn seed guide` is where the craft lives. The launch-injected garden block
// carries the rules an agent follows without asking; everything that takes
// judgment rather than obedience is here, on demand, so the always-on block
// stays light and there is one place to keep the craft current. The text is
// transplanted from the attn skill's garden and delegated-agent references,
// which now point here.

func runSeedGuide(args []string) {
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "seed guide: takes no arguments\n")
		os.Exit(2)
	}
	writeSeedGuide(os.Stdout)
}

func writeSeedGuide(w io.Writer) {
	fmt.Fprint(w, seedGuideText)
}

const seedGuideText = "The garden holds the work. Syntax: `attn seed --help`.\n\n" + `WRITING A BODY

The body is the brief. A delegate dispatched at the seed gets it as its
prompt, and whoever picks the seed up later reads it with none of what you
know now. Write it for that reader.

- Outcome first. What done looks like, not a procedure. "X is the only
  backend the daemon talks to, the old path is deleted, tests green", not
  "migrate the store to X".
- Just enough context. The paths, the one non-obvious constraint, the why.
- Verification. How completion is known and what evidence gets attached.
- Scope. What is deferred, and what is a blocker versus a call the tender
  makes alone.

The body is the contract, still true when somebody else tends the seed
tomorrow. The log is the live thread. What happens along the way goes in
notes and steering, not in the body.

WRITING A PLAN

A plan is a plot: the body is the plan, each child is one unit somebody can
tend alone, and ` + "`blocks`" + ` is the only ordering. Leave children parallel unless
one truly needs another's result; every edge is a wait.

The body is read by whoever comes next with none of what you know now: an
agent starting cold, or the user at the end of a long day. Both skim it to
decide what to do and what to change. Every section survives a skim: the
point first, short prose, the smallest picture that makes it clear. Lead
with the choices the user would change on review (the data model, the
interfaces, what people will see); the mechanical work goes last. A body is
a review surface before it is a to-do list.

The default shape, trimmed or grown as the work needs:

- Goal. What done looks like, as an outcome. One paragraph.
- Shape. The implementation shape in repo terms, not generic layers, as a
  picture (below).
- Data model and interfaces. Only when the work crosses a boundary: the
  records, config, state and messages that cross it, and which side
  creates, owns, or only reads each. Loose pseudocode beats exact types.
- Boundaries. What each component owns and what it must not know about.
- Decisions. Three to five, each with its reason: the choices that would
  surprise the next implementer, or where a plausible alternative was
  rejected.
- Open questions. What is a blocker and needs the user, versus a call the
  tender makes.

A call stack is usually the cheapest picture with the most payoff: who calls
whom, in what order, where the boundaries sit.

    handleSubmit
      createSession
        persistPrompt
        launchAgent
      navigateToSession

When the code exists, show what changes as a diff over its shape rather than
before and after copies.

    handleSubmit
      createSession
        persistPrompt
    +   expandSkillMention
        launchAgent

Pick the picture by what the reader must see: a call stack for control flow
and ownership; a shallow file tree with one-line responsibilities for a broad
refactor; a component tree for UI; a mermaid sequence diagram for anything
crossing a process, network or queue. Text trees and mermaid both render when
the body is opened in attn. Show production and test wiring when they
differ. One or two pictures is typical; a picture that needs studying has
failed. Prose that restates a picture is waste: a sentence introduces it and
stops. Put each picture next to the claim it supports, never in an appendix.
Use pseudocode and small code examples freely when they tell the simpler
story.

The children are the steps. Their bodies follow the rules for any body and
do not repeat the plan; they point at it. Their states are the progress, so
the plan carries no checklist. When the work forces a deviation, take the
conservative option, note it on the plot with what triggered it, and keep
going; a silent deviation is how the next plan repeats the mistake. Deferred
work is a seed, planted under the plot or beside it, not a paragraph.

    attn seed plot -f plan.json              all of it in one move; plan.json looks like

    {
      "title": "Search moves to the daemon",
      "body": "# Search moves to the daemon\n\n## Goal\n... (the plan, as above)",
      "children": [
        {"title": "Daemon search endpoint", "body": "..."},
        {"title": "App calls the endpoint", "body": "...", "blocks": ["remove-the-client-index"]},
        {"title": "Remove the client index", "body": "..."}
      ]
    }

    attn seed plant "…" --part-of <plot>     one more child, later
    attn delegate --brief "…" --plot <plot>  a tender for the whole plot; its ready
                                             answers from the plot, oldest first
    attn seed edit <id> -m "…"               the plan changed; say what in a note
    attn open <plot>                         read it rendered, the way the user does

Before planting, check: can an implementer name the first files to edit from
the body alone? Would a thirty-second skim of the headings and pictures give
the goal and the shape? Is prose doing work a small tree would do better?

WHAT DONE IS, BY DELIVERABLE

A few examples; the shape carries to any deliverable.

    code       behavior exists, tests green, PR up. Prescribe the outcome and
               the constraints, not the implementation, unless the user hands
               you the design too (an API contract, a call stack) so the tender
               does not have to invent one.
    bug fix    root cause found, then fixed, with a regression test. Give the
               symptom and a repro only; prescribing the fix invites
               symptom-patching.
    research   a sourced answer that feeds a decision. Frame the question, not
               a task.
    docs       the point made durably, the old text superseded. Give the
               audience, what it replaces, the one idea.
    refactor   the code issue is gone, behavior unchanged. Name the issue
               being fixed: the duplication, the function doing two jobs,
               the module that knows too much.
    prototype  a decision or a feel, then thrown away. Name the question it
               answers; tests optional.

Harvest on evidence, the user accepted it or the PR merged, not on the type.
Implementation finished but acceptance pending is a note, and the seed stays
open.

ARTIFACTS

You attach a document to a seed. The document stays where it lives:

    attn seed attach <id> --path <file.md> [--repo <repository>]
    attn seed attach <id> --notebook <document-id>
    attn seed attach <id> --url <url>
    attn seed detach <id> --path <file.md>

Edit only the original, and note a meaningful edit, rename or deletion on the
seed so the next reader knows to re-read it.

HANDOFFS AND STEERING

    attn seed note <id> -m "…" --handoff   for the next tender; attn seed show prints
                                            it first, attn seed tend prints it on the claim
    attn agent msg <seed-id> "…"            reaches whoever tends it now; an
                                            untended seed refuses by name

Leave a handoff whenever you park a seed, or stop mid-thread and do not
intend to continue: outcome, evidence, next action. Long reasoning goes in an
attached artifact, not in the note. Noting does not end your session; keep
working unless blocked or done.
`
