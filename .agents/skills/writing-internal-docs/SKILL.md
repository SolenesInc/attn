---
name: writing-internal-docs
description: Use before editing any file in docs/internal/.
---

# Writing internal docs

`docs/internal/` holds attn's maintainer docs. AGENTS.md sends agents to them
before they plan or change the part of attn a doc covers. A wrong or unclear
sentence here becomes a wrong plan or a wrong change.

- Read the whole doc before you edit it, and judge your change against the
  text as a whole. When other docs cover the same subject, read those passages
  too.
- Each doc says what it is for in its opening. Keep your change to that
  purpose. For example, `harnesses.md` records harness gotchas and
  undocumented behavior, not attn's behavior.
- Check every claim you add or rewrite against the code.
- The readers are agents, and they read literally. Follow the
  technical-writing reference in the attn-workflow skill
  (`internal/prompts/content/skills/attn-workflow/references/technical-writing.md`).
- Use the terms in `glossary.md` without defining them again. Edit
  `glossary.md` with the `glossary` skill.
