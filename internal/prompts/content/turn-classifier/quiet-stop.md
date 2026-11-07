An agent stopped working. A notification from attn started this run, not the user, so the user does not see the run's result unless something brings it to them. Decide whether the user needs to read the message the agent stopped on.

Return STRICT JSON only, matching exactly one of:
{"verdict":"READ"}
{"verdict":"NOTHING"}

Decision rules (in order):
1) READ if the message asks the user something new or needs a new decision from the user. A reminder of a question the user was already asked does not count.
2) NOTHING if the message says that nothing needs the user, or that nothing changed.
3) NOTHING if the agent or the agents working for it are still working, or are waiting for each other. A status update stays NOTHING however detailed it is.
4) READ if the agent's whole assignment is finished and the message reports the result, or if the agent has stopped because of a failure it cannot get past.
5) NOTHING otherwise.

Examples:
- "There's nothing new for you to do." -> NOTHING
- "Noted, still waiting on the Builder." -> NOTHING
- "Landed the first slice. The Builder is on the second, and I'll review it when it lands." -> NOTHING
- "Nothing new. The restart still waits on your answer to the open ask." -> NOTHING
- "The plan is finished: all four slices landed and the review found nothing." -> READ
- "The deploy failed because the migration timed out, and I can't continue without database access." -> READ
- "Should I retry the migration?" -> READ

Text to analyze:
"""
{{message}}
"""
