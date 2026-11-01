# Derived things

Principles this process relies on: [Derived things must earn their place](../principles/derived-things-must-earn-their-place.md), [Subtract before you add](../principles/subtract-before-you-add.md), [Encode lessons in structure](../principles/encode-lessons-in-structure.md). Read them before you start. [The skill's index](../SKILL.md#principles) lists the rest.

Use this process before you add something derived from the work, such as
documentation, a code comment, a summary, a generated file or a copy of state,
and when you build or change what keeps the derived thing current.

## Look for a better option first

Each option below removes the need instead of managing
the need. Take the first one that works:

1. Put the intent in the work itself: a name, a type, a structure or a test. A
   comment that explains code means the code needs rewriting.
2. Point at the source instead of copying it: a path, a link, or a command that
   prints the current value.
3. Derive the value when you need it instead of storing it.

Keep a derived thing only when it holds what the work cannot express: why a
design was chosen, a constraint set outside the codebase, the vocabulary the
user and agents share, a guide a person reads before touching the work.

## Keep it current at the highest level that works

Go down a level only when the level above cannot keep the derived thing
current:

1. **Generate it.** Build it from the source and fail the build when the
   committed copy differs, as `make generate-types` and `make check-types` do.
2. **Check it mechanically.** A test or lint compares it with the source and
   names the mismatch.
3. **Check it with an agent when the source changes.** Write a
   `verify-<thing>` skill that agents follow at a step they already take, such as delivering a
   change or editing the file. `verify-docs`, `verify-tests`, and `glossary`
   are examples.
4. **Check it on a schedule.** When no single change causes the drift, an
   automation audits the derived thing regularly.

A derived thing with none of these becomes wrong without anyone noticing. Delete
it rather than ask agents to remember it.

## Write a `verify-<thing>` skill

- **Trigger.** Name the skill `verify-<thing>`, and say in its description which
  change triggers it: "Use before landing or opening a pull request whose
  changes edit …", or "Use when editing …".
- **Scope.** List what the skill covers and where the source of truth lives.
  Point at the source's own rules instead of repeating them.
- **Read.** Read the derived thing whole, the source it comes from, and the
  other places that cover the same subject.
- **Run.** Execute what can be executed. When you doubt a check can fail, break
  the source in a scratch worktree and watch the check fail.
- **Judge.** Ask named questions, and answer each with evidence.
- **Report, don't edit.** Give the place of each finding, the evidence, and the
  full replacement text. Whoever made the change applies the replacement.
- **Drift or regression.** When the derived thing and the work disagree, decide
  which one is wrong. Fix drift in the derived thing. Report a regression in
  the work. Never edit the derived thing to match the regression.
- **Approval.** When a person owns the meaning, as the user owns the glossary,
  say who approves and how.

## Prove it

Prove the check as [Correction](correction.md#prove-it) describes: show the
check failing on a real past drift, then passing on the fix. When the same
drift comes back, it is a class: fix it with Correction.
