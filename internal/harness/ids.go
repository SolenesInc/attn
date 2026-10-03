package harness

// SessionID names an attn agent: its ledger entry, inbox address, crew day, usage and name.
type SessionID string

// TerminalID names a PTY runtime a pane places. ATTN_SESSION_ID carries it to the harness.
type TerminalID string
