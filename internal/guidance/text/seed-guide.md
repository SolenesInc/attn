The garden holds the work. Syntax: attn seed --help.

WRITING A BODY

The body is the brief. A delegate dispatched at the seed gets it as its prompt, and whoever picks the seed up later reads it with none of what you know now. Write it for that reader.

- Outcome first. What done looks like, not a procedure.
- Just enough context. The paths, the one non-obvious constraint, the why.
- Verification. How completion is known and what evidence gets attached.
- Scope. What is deferred, and what is a blocker versus a call the tender makes alone.

WRITING A PLAN

A plan is a plot: the body is the plan, each child is one unit somebody can tend alone, and `blocks` is the only ordering. Leave children parallel unless one truly needs another's result; every edge is a wait.

Lead with the choices the user would change on review. Show control flow or ownership with the smallest picture that makes it clear. The children are the steps; their states are progress, so the plan carries no checklist.

WHAT DONE IS, BY DELIVERABLE

    code       behavior exists, tests green, PR up
    bug fix    root cause found, then fixed, with a regression test
    research   a sourced answer that feeds a decision
    docs       the point made durably, the old text superseded
    refactor   the code issue is gone, behavior unchanged
    prototype  a decision or a feel, then thrown away

Harvest on evidence, the user accepted it or the PR merged, not on the type. Implementation finished but acceptance pending is a note, and the seed stays open.

ARTIFACTS

    attn seed attach <id> --path <file.md> [--repo <repository>]
    attn seed attach <id> --notebook <id>
    attn seed attach <id> --url <url>
    attn seed detach <id> --path <file.md>

HANDOFFS AND STEERING

    attn seed note <id> -m "..." --handoff
    attn agent msg <seed-id> "..."

Leave a handoff whenever you park a seed or stop mid-thread: outcome, evidence, next action. Long reasoning goes in an attached artifact. Noting does not end your session.
