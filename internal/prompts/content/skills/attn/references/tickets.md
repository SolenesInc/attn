# Tickets retired

Work lives in the garden. Read [garden.md](garden.md).

Every `attn ticket` write verb is a signpost: run it and it names the garden
command that replaced it, then exits nonzero. Nothing creates a ticket any
more — a delegation binds a seed, and unbound backlog tickets were converted to
seeds at the cutover.

Three reads remain for tickets that predate the garden:

- `attn ticket list [--all]` — the archived board.
- `attn ticket show <ticket-id>` — one ticket's full record.
- `attn ticket inbox` — unread activity for this session; reading acknowledges it.
