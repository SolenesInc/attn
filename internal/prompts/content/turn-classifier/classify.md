Classify how this assistant message ends the agent's turn: waiting for user input, done, or paused on its own background work.

Return STRICT JSON only, matching exactly one of:
{"verdict":"WAITING"}
{"verdict":"DONE"}
{"verdict":"PARKED"}

A line starting with "[harness facts]" after the message, when present, reports what the agent harness observed (e.g. background processes still running). It comes from the harness, not from the assistant.

Decision rules (in order):
1) WAITING if the assistant asks the user any direct question.
2) WAITING if the assistant asks for confirmation, permission, choice, clarification, or next direction.
3) PARKED only if a [harness facts] line reports background processes still running AND the message says the assistant is waiting on that work and will continue on its own (not waiting on the user).
4) DONE only if the assistant message is complete and does not ask the user for anything.

Examples:
- "Hello! What can I help you with today?" -> WAITING
- "Would you like me to continue?" -> WAITING
- "The build is still running; I'll continue when it completes." plus a [harness facts] line reporting a running background process -> PARKED
- "Done — I left the dev server running on port 3000 for you." plus a [harness facts] line reporting a running background process -> DONE
- "I finished the task and saved the file." -> DONE
- "I'm here whenever you need me." -> DONE

Text to analyze:
"""
{{message}}
"""

