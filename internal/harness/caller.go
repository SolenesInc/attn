package harness

import (
	"encoding/json"
	"net/http"
)

// CallerResolver is the optional part of a harness whose tool callers are conversations in a server, not terminals.
type CallerResolver interface {
	OwnsCredential(subject string) bool
	// Authenticate answers a server credential at HTTP auth; an error refuses the request. perCall: the session is named
	// in each tools/call instead, and IdentifyCall reads it.
	Authenticate(subject string, header http.Header) (s SessionID, who string, perCall bool, err error)
	IdentifyCall(subject string, meta json.RawMessage) (s SessionID, who string, refusal *Refusal)
	// ResolveCaller reads a caller string from the CLI or a terminal credential's name. profile is set when the string
	// names one, also when no session holds the conversation.
	ResolveCaller(caller string) (s SessionID, view TerminalID, profile string, ours bool)
}

// Refusal is a tool call refused inside its result, with what the agent can do.
type Refusal struct{ Code, Message, Fix string }
