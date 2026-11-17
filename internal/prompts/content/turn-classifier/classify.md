Classify how this assistant message ends the agent's turn: waiting for user input, done, or paused on its own background work.

Return STRICT JSON only, matching exactly one of:
{"verdict":"WAITING"}
{"verdict":"DONE"}
{"verdict":"PARKED"}

A line starting with "[harness facts]" after the message, when present, reports what the agent harness observed, for example background processes still running. It comes from the harness, not from the assistant. It names each background item. Some end on their own and report back to the assistant, such as a build, tests, a subagent, or a wait for CI or a deploy. Others only fire when someone else acts, such as a watch for comments or live updates, or a dev server. This line alone never makes a turn PARKED. Decide from what the message says.

Decision rules (in order):
1) WAITING if the assistant asks the user any direct question.
2) WAITING if the message leaves the next step to the user: it asks for confirmation, permission, a choice, clarification or direction, names a decision that is theirs, or asks them to do something and report back.
3) PARKED only if the message itself says the assistant is waiting on work that is still running and will continue on its own when it finishes, AND the [harness facts] line names work that ends on its own. A watch that waits for someone else to act never makes a turn PARKED.
4) DONE otherwise: the message reports finished work or a result, and does not ask the user for anything.

Examples:
- "Hello! What can I help you with today?" -> WAITING
- "Would you like me to continue?" -> WAITING
- "One decision is yours: ship it now or wait for review." plus a [harness facts] line -> WAITING
- "Run the deploy command, and tell me when it finishes." plus a [harness facts] line -> WAITING
- "The build is still running; I'll continue when it completes." plus a [harness facts] line naming a running build -> PARKED
- "Done — I left the dev server running on port 3000 for you." plus a [harness facts] line naming the dev server -> DONE
- "I updated the plan and the page to match." plus a [harness facts] line naming a watch for live updates -> DONE
- "I finished the task and saved the file." -> DONE
- "I'm here whenever you need me." -> DONE

Text to analyze:
"""
{{message}}
"""
