---
name: verify-docs
description: Use before landing or opening a pull request whose changes edit docs, agent guidance, skills, product prompts or CLI help.
---

# Verify docs

Agents often read a small part of a file and add to it, ignoring the text
around it. The result repeats, contradicts or buries what was already there.
Check that each changed text still reads as one coherent whole. Report
findings; do not edit.

It covers:

- Markdown anywhere in the repository, except changelogs
- product prompts in `internal/prompts/**` and the Go definitions that compose
  them
- CLI help

## Read

1. List the changed text: `git diff --stat "$(git merge-base HEAD origin/main)"`
   and the new files in `git ls-files --others --exclude-standard`.
2. Read each changed file whole as it is now, and as it was on the base.
3. Find the other places that cover the same subject. Search docs, every
   `AGENTS.md`, `.agents/skills` and `internal/prompts/content` for the terms
   the change uses, and read those passages.
4. For product prompts, read what each recipient receives:
   `go run ./cmd/prompt-editor compare --base origin/main --json`, then
   `context` with the same base for each affected event. Apply the
   after-editing check in `cmd/prompt-editor/authoring.md`.
5. For CLI help, read the changed command's whole help and its sibling
   commands' help.
6. For the glossary, also apply the glossary skill's rules.

## Judge each change

- Does it say again what this text or another place already says? Name the
  place that should own it.
- Does it contradict the rest of the text, another doc, the glossary or the
  code?
- Does it belong where it was added? A new paragraph at the end, or a new
  rule beside an older one on the same subject, usually means the older text
  needed a rewrite instead.
- Does the whole text still read in order: terms defined before they are used,
  sections that follow from each other, lists that stay parallel?
- Does anything now refer to a renamed or removed thing, or give an example
  that is no longer true?
- Does it use glossary terms, and no legacy names?
- Does it follow `AGENTS.md`: no implementation notes, product prompts address
  the user?
- Text that agents load in every session, like `AGENTS.md`, the glossary and
  priming prompts, costs context everywhere. Does each added line earn that?

## Report

For each finding, give:

- the file and the passage
- the problem, quoting both places for a duplication or contradiction
- the full rewritten passage, written as one coherent text, not a patch on
  the patch

If no change has a finding, say so in one line.
