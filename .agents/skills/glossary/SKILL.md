---
name: glossary
description: Use when editing attn's glossary.
---

# Glossary

Every change to docs/glossary.md must be approved by the user, line by line.

## Why

The glossary is attn's domain model, in the sense of domain-driven design. Its
terms are the ubiquitous language that the user and the agents share.

We coin names on purpose, to avoid collisions with the user's own world: a seed
is not the ticket in the user's Jira. Humans adapt to coined names quickly, from
context and from the actions around them. Agents do not adapt: they read every
word through the weights of their model. So the glossary maps each coined name
to the established term that carries its meaning for an agent.

AGENTS.md tells every agent to read the glossary. So every line costs context
in almost every session. Keep it short.

## Language

Use "80% of ASD-STE100" when you write the glossary and when you talk through
domain concepts with the user.

## Rules

1. The trees come first. Each tree is one model of attn.
2. Every term attaches to a tree. If a term does not attach, it is the wrong
   word or it needs a new tree. A new tree needs the user's approval.
3. Each relationship in a tree states its count: 1, 0..n, at most 1.
4. A definition uses only terms that the glossary defines, established terms, or
   plain English.
5. Define words, not behavior. Timers, limits and sequences go in other docs.
   The glossary can link to them.
6. Keep each term's text to at most three short sentences. If you need more, use
   a list or a tree.
7. One name per thing. Before you add a term, search the glossary for a word
   that already names the same thing.
8. No tables.
9. Use they/them for agents and crew members, not it.
10. When editing the glossary, always consider the change holistically.
11. Each coined name's definition maps it to the established term that carries
    its meaning (seed → ticket, plot → epic). Choose established terms with care:
    they bring all their associations with them.

## Tech debt

The glossary ends with a Tech debt section. It has legacy names and changes to
make. Put code changes in "Changes to make", not in seeds.

### Legacy names

Some old names are still in code, docs and the wire. This is temporary debt that
the user will pay soon. The glossary maps each old name to its new term.

- When you read an old name, understand it as the new term.
- Never write an old name in new text: docs, prompts, UI, comments or talk with
  the user.
