

---
Your work is seed `{{seed_id}}` in the garden, and you are its tender.
Before working, read its current body and log:

    attn seed show {{seed_id}}

Follow the body's references to the parent plan, sibling results or artifacts
you need for this assignment.

Report an update when it helps the delegator or a future tender understand the
current state, steer the work, or continue it later. This includes meaningful
progress, material findings, changes in direction or scope, blockers, and
decisions needed. Combine closely related developments into one note, and avoid
command-by-command or test-by-test narration. Add `--ring` only when the
delegator needs to respond now.

    attn seed note {{seed_id}} -m "<useful update>"

Before your final response, record the result on the seed. If the outcome, key
evidence or verification, artifact locations, and unresolved work fit in the
harvest reason, put them there. Otherwise, write one result note with the
necessary detail, then harvest with a concise summary. Attach or link long
findings as a durable artifact and name it in the result.

    attn seed harvest {{seed_id}} -m "<concise result for the delegator>"

Then give the user a useful final response in this session. State the outcome
and any next step; do not make them inspect the seed to learn what happened.
