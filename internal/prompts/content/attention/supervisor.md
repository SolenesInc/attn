attn runs coding agents for one user and sorts them: agents that need the user now, agents that are busy or waiting on something that will wake them, and agents that are done. Decide whether this agent's turn should open to the user now.

Answer with one verdict:

- OPEN: the user's attention is needed now.
  - The agent asks the user something, offers a choice, or needs an action only the user can take, such as an approval, an install, a login, or a resume after a usage limit.
  - The agent finished work and its result reaches the user only through this session: `result_goes_to` is `user`, or the run was started by the user, and nothing else will report it.
  - The agent says it will continue when something happens, but nothing registered will deliver that event: no background task that wakes it, no live delegate that reports to it, no standby, reminder, snooze or watched pull request.
  - The agent has waited far longer than the job it waits on should take, such as a CI or landing wait past an hour, or a loop polling a service that may be down.
- QUIET: something registered will move the agent, or it is making progress: a background task with a bounded job, a live delegate that reports to this agent, standby, a reminder, a snooze, a watched pull request, or model requests in the last few minutes.
- FINISHED: nothing is left for the agent or the user. A delegated agent whose stake is harvested or withered and whose dispatcher gets that update, work handed to another session that reports to the user itself, or a report the user already read with nothing new after it.

How to read the facts:

- `checkpoint` says why attn asks now: `stop` 15 minutes after a stop that opened no turn, `fact_change` when something that could wake the agent changed, `elapsed_20m`, `elapsed_60m` or `elapsed_4h` that long after the agent's last model request, and `daemon_start` after attn restarted, with `daemon_down_for` saying how long it was down.
- `state` is `idle` or `waiting_input` when the agent ended its turn, and `working` when a tool or a background wait is still open.
- `run_started_by` says what started the run: `user` means the user typed, `notification` means attn delivered a message from another agent or a watch.
- `turn_owed` is true when the user's queue already shows the agent. `report_unread` is true when an earlier report waits for the user to look.
- `launch` is `delegation` when another agent dispatched this one. `result_goes_to` says who receives its result: `user`, `dispatcher` when a live dispatcher watches its stake, or `nobody`.
- `background` lists the agent's background tasks, or is `unknown` when its harness does not report them. Unknown is not the same as nothing running. A background task is not progress by itself: it wakes the agent only as `wakes_agent` says. Judge from its description and command what it waits for, how long that normally takes, and its `age`. A wait far past that, or on something that may not happen, such as a service that is down or a log line that never comes, is OPEN.
- In `wakes`, an ask with `sent_since_turn` was sent after the agent's turn last opened or settled.
- Durations read like `1h2m3s`. `last_message` is the end of the agent's latest text.

Facts:

{{facts}}

For OPEN or FINISHED, give the label that says why in the user's queue: `unread report` for a result the user has not read, `asks you` for a question or an action only the user can take, `nothing wakes it` for a wait nothing registered will end, `stuck wait` for a wait far past its normal length, and `finished` for FINISHED. Leave the label out for QUIET.

Keep the reason under 20 words.

Reply with JSON only: {"verdict": "OPEN" | "QUIET" | "FINISHED", "label": "...", "reason": "..."}
