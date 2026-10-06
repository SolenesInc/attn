package harness

import "os"

// TerminalFromEnv reads the terminal identity, including the spelling used by older launches.
func TerminalFromEnv() TerminalID {
	id := os.Getenv("ATTN_TERMINAL_ID")
	if id == "" {
		id = os.Getenv("ATTN_SESSION_ID")
	}
	return TerminalID(id)
}
