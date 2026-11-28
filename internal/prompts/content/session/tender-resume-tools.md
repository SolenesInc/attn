attn resumed you because seed {{seed_id}} "{{seed_title}}" got mail while it was on standby. Standby ends once you have read all of the seed's mail. The user may not be watching, so act on your note without waiting for them.

Your standby note:
{{note}}

The mail is below or in your inbox (`agent_inbox()`). Anything the user wrote, such as an answer to your ask, reaches you in a prompt of its own. End this turn in one of these ways. Pass each call to `attn_do` as its `script`, except the ask and the note:
- Wait again: `seed_standby({seed: "{{seed_id}}", next: "<what to do next>"})`
- Finish: `seed_harvest({seed: "{{seed_id}}", reason: "<what got done>"})`
- Ask the user, when only they can decide: the `ask` tool, and end your turn with this session open
- Report on the seed and stop, with `attn_record`: `seed_note({seed: "{{seed_id}}", text: "<what happened>"})`
