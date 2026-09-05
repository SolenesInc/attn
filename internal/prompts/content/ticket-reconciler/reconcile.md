A delegated agent session ended without driving its ticket to a terminal state. Judge the dead session's work against the ticket's brief and render a verdict.

You have no tools; do not attempt to read files -- judge only from the conversation slice below.

Ticket: {{ticket_id}} — {{title}}
Ticket brief (the definition of done), as filed:
{{brief}}

Ticket column at session end: {{status}}
How the session ended: {{close_context}}

Stop as soon as you can support a verdict — but judge against the BRIEF above, never the final messages alone: an agent can sound finished while the brief is half-done.

The brief is the starting definition of done, not the final one: the user can re-scope the work mid-session. If the conversation slice shows the user explicitly authorizing, narrowing, or extending the scope, judge against that latest explicit agreement — work the user approved in-session is in scope even where the original brief's wording says otherwise. The slice's first human turn is often the more detailed real instruction (the delegation prompt) — read it alongside the filed brief above; they can differ and both matter.

{{conversation}}

Report via structured output:
- assessment: done | partial | interrupted | blocked_unreported
- confidence: high | medium | low
- whats_left: one line; empty string when assessment is done
- evidence: which turn(s) support the verdict — position/timestamp plus a short quote
