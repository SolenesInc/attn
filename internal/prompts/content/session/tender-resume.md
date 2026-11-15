attn resumed you because seed {{seed_id}} "{{seed_title}}" got mail while it was on standby. Standby ends once you have read all of the seed's mail. The user may not be watching, so act on your note without waiting for them.

Your standby note:
{{note}}

The mail is below or in your inbox (`attn agent inbox`). End this turn in one of these ways:
- Wait again: `attn seed standby {{seed_id}} -m "<what to do next>"`
- Finish: `attn seed harvest {{seed_id}} -m "<what got done>"`
- Ask the user, when only they can decide: `attn ask`
- Report on the seed and stop: `attn seed note {{seed_id}} -m "<what happened>"`
