# Classifier

This package decides an agent's state after a turn (`idle`, `waiting_input`,
`parked`, `unknown`) from the assistant's last message. The verdict comes from
an LLM and the parser of its reply. No keyword lists, no regex over the
assistant text, no fallback heuristics: when the LLM output is missing or
unparseable, return `unknown`.
