package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/victorarias/attn/internal/harness"
)

func (codexHarness) OwnsCredential(subject string) bool {
	return strings.HasPrefix(subject, sharedCodexServerPrefix)
}

func (codexHarness) Authenticate(subject string, _ http.Header) (harness.SessionID, string, bool, error) {
	return "", "shared Codex of profile " + strings.TrimPrefix(subject, sharedCodexServerPrefix), true, nil
}

func (h codexHarness) IdentifyCall(subject string, meta json.RawMessage) (harness.SessionID, string, *harness.Refusal) {
	profile := strings.TrimPrefix(subject, sharedCodexServerPrefix)
	var named struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(meta, &named)
	conversation := strings.TrimSpace(named.ThreadID)
	if conversation == "" {
		return "", "shared Codex of profile " + profile, &harness.Refusal{Code: "no_conversation", Message: "the call's _meta names no Codex conversation in threadId", Fix: "Call the tool from a Codex conversation; Codex names it in _meta.threadId."}
	}
	who := "shared Codex conversation " + conversation
	session, _, _ := h.d.codexShared().resolveCaller(harness.ServerCaller{Variable: harness.CodexThreadEnv, Scope: profile, Conversation: conversation})
	if session == "" {
		return "", who, &harness.Refusal{Code: "no_session", Message: fmt.Sprintf("no session of profile %s holds Codex conversation %s", profile, conversation), Fix: "Call the tool from a conversation that an attn session shows."}
	}
	return session, who, nil
}

func (h codexHarness) ResolveCaller(caller string) (harness.SessionID, harness.TerminalID, string, bool) {
	c, ok := harness.ParseServerCaller(harness.TerminalID(caller))
	if !ok || c.Variable != harness.CodexThreadEnv || c.Scope == "" {
		return "", "", "", false
	}
	session, view, _ := h.d.codexShared().resolveCaller(c)
	return session, view, c.Scope, true
}
