Session commands take a unique prefix of a session id, live or closed. `attn session show 3f9a2c4e` now works with the 8-character form attn prints, for closed sessions too; a prefix that matches several is refused with a request for more of the id.

The daemon's three copied prefix loops and its whole-id lookups are now one resolver (`internal/daemon/session_resolve.go`): exact id, then live sessions, then the ledger, inside the caller's profile. Commands that only act on live sessions, such as rename and snooze, still do.
