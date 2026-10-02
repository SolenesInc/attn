
# Align

Build shared understanding. Make your interpretation visible to the user, uncover differences, and challenge assumptions to strengthen the idea.

Read and use [discuss](discuss.md) for the interview: investigation, question rounds, and recommendations. Apply the alignment concerns below within that conversation, and close with the reflection under Finish in conversation.

Scale the check to what is still unsettled. Before a consequential step in work you have already discussed, reflect your understanding, the decisions you made on your own, and the assumptions the next step relies on, and ask only where you and the user could differ.

## Investigate

Form your own understanding of the intent, desired outcome, scope, and unresolved choices. Resolve what you can from the code, docs, and existing conversation. Distinguish what the evidence supports from what you are assuming.

For proposed work, examine how it fits the system and what happens in failures or unusual states, including the user's experience. If the work references a vision or the user supplies one, read it and check the idea against its desired outcomes, values, and scope. Use `attn seed show <id>` for vision seeds. Bring conflicts into the conversation.

## Question and explore

- State the assumption or interpretation behind each question so the user has something concrete to correct. Ask where intent, meanings, priorities, or expected behavior could differ.
- Revisit earlier assumptions when later answers expose a conflict.
- Use concrete scenarios, counterexamples, and alternatives to test the idea and any apparent agreement. Explain the consequence behind a challenge and revise your own view when the answer changes it.
- When a quality is easier to recognize than describe, ask for a reference or suggest comparing variants in a [prototype](prototype.md). Prefer source code, then screenshots, then adjectives when available and relevant.

For example:

> I'm assuming users should retain control even when automation would be faster. If those conflict, where would you draw the line?

## Finish in conversation

When the questions have resolved the important differences or made the remaining uncertainty explicit, reflect the shared understanding and unresolved points briefly in chat. Do not manufacture further questions once they stop adding understanding.

Record shared understanding, decisions, and open questions as the conversation develops. Keep tentative ideas distinguishable from agreed decisions.

The goal is shared understanding before anyone acts on it. Let the user choose the next step.
