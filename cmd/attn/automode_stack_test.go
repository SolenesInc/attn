package main_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/testworld"
)

type autoModeProposed struct {
	Proposal struct {
		Kind    string `json:"kind"`
		Summary string `json:"summary"`
	} `json:"proposal"`
}

type autoModeDenials struct {
	Denials []struct {
		CreatedAt string `json:"created_at"`
		SessionID string `json:"session_id"`
		Rule      string `json:"rule"`
		Signature string `json:"signature"`
		Reason    string `json:"reason"`
	} `json:"denials"`
	LedgerNote string `json:"ledger_note"`
}

func writeAutoModeDenialLedger(t *testing.T, s *testworld.Stack, records ...any) {
	t.Helper()
	var ledger strings.Builder
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		ledger.Write(line)
		ledger.WriteByte('\n')
	}
	if err := os.WriteFile(automode.DenialLedgerPath(s.Dir), []byte(ledger.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireInOrder(t *testing.T, what, line string, fields ...string) {
	t.Helper()
	rest := line
	for _, field := range fields {
		i := strings.Index(rest, field)
		if i < 0 {
			t.Errorf("%s does not show %q after the fields before it:\n%s", what, field, line)
			return
		}
		rest = rest[i+len(field):]
	}
}

func TestAutoModeRecordsRuleProposalsFromTheirTokensAndListsDenials(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"denials", "--limit", "0"}, want: `automode denials: --limit wants a positive number, got "0"`},
		{args: []string{"denials", "--limit=many"}, want: `automode denials: --limit wants a positive number, got "many"`},
		{args: []string{"denials", "5"}, want: `automode denials: unexpected argument "5"`},
		{args: []string{"rule", "add", "--json"}, want: "automode rule add: name the command tokens"},
		{args: []string{"rule", "add", "--decision", "prompt"}, want: "automode rule add: name the command tokens"},
		{args: []string{"rule", "add", "--sandbox", "bypass"}, want: "automode rule add: name the command tokens"},
	} {
		refused := s.Attn(append([]string{"automode"}, tc.args...)...)
		if refused.Code != 2 || refused.Stdout != "" || !strings.HasPrefix(refused.Stderr, tc.want) {
			t.Errorf("attn automode %s exited %d with stderr %q, want 2 and %q", strings.Join(tc.args, " "), refused.Code, refused.Stderr, tc.want)
		}
	}

	s.Start()
	requireStdout(t, s.Attn("automode", "denials"), "no denials recorded\n")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"--json", "git", "push", "origin"}, want: "allow, bypass sandbox: git push origin"},
		{args: []string{"git", "fetch", "origin", "--json"}, want: "allow, bypass sandbox: git fetch origin"},
		{args: []string{"--json", "--decision", "prompt", "--justification", "rewrites history", "git", "rebase"}, want: "prompt, inherit sandbox: git rebase"},
		{args: []string{"--json", "--sandbox=inherit", "cargo", "test"}, want: "allow, inherit sandbox: cargo test"},
	} {
		var proposed autoModeProposed
		s.Attn(append([]string{"automode", "rule", "add"}, tc.args...)...).JSON(t, &proposed)
		if proposed.Proposal.Kind != "rule" || proposed.Proposal.Summary != tc.want {
			t.Errorf("attn automode rule add %s proposed %+v, want a rule %q", strings.Join(tc.args, " "), proposed.Proposal, tc.want)
		}
	}
	recorded := s.Attn("automode", "rule", "add", "--sandbox", "inherit", "go", "vet")
	requireStdout(t, recorded, "recorded proposal ", ": rule allow, inherit sandbox: go vet\n",
		"This changed nothing yet. Promote it in the attn app to put it in force.\n")
	shown := s.Run(testworld.Invocation{Args: []string{"automode", "show"}, Dir: s.Dir})
	requireStdout(t, shown, "pending proposals (promote them in the attn app):\n",
		"allow, bypass sandbox: git push origin", "allow, bypass sandbox: git fetch origin",
		"prompt, inherit sandbox: git rebase", "allow, inherit sandbox: cargo test", "allow, inherit sandbox: go vet")

	denial := func(session, action, reason, at string) map[string]string {
		return map[string]string{"session_id": session, "tool": "bash", "action": action, "reason": reason, "rule": "classifier-2a", "at": at}
	}
	writeAutoModeDenialLedger(t, s,
		map[string]any{"type": "rotated", "dropped": 3, "at": "2026-08-18T09:00:00.000Z"},
		denial("pi-1", "bash: curl https://example.com", "the user never asked to reach that host", "2026-08-18T10:00:00.000Z"),
		denial("pi-2", "bash: git push --force", "force pushes rewrite shared history", "2026-08-18T11:00:00.000Z"),
	)
	var listed autoModeDenials
	s.Attn("automode", "denials", "--json").JSON(t, &listed)
	if len(listed.Denials) != 2 || listed.Denials[0].SessionID != "pi-2" || listed.Denials[1].SessionID != "pi-1" {
		t.Fatalf("automode denials --json = %+v, want both denials newest first", listed.Denials)
	}
	table := s.Attn("automode", "denials")
	requireStdout(t, table, "note: 3 older denials were dropped when the local ledger rotated\n")
	rows := strings.Split(strings.TrimSuffix(table.Stdout, "\n"), "\n")
	if len(rows) != 3 {
		t.Fatalf("automode denials printed %d lines, want a row per denial and the note:\n%s", len(rows), table.Stdout)
	}
	for i, d := range listed.Denials {
		requireInOrder(t, "denial row", rows[i], d.CreatedAt, d.SessionID, d.Rule, d.Signature, d.Reason)
	}

	spaced := s.Attn("automode", "denials", "--limit", "1")
	joined := s.Attn("automode", "denials", "--limit=1")
	requireStdout(t, spaced, "pi-2", "note: 3 older denials")
	if strings.Contains(spaced.Stdout, "pi-1") || joined.Code != 0 || joined.Stdout != spaced.Stdout {
		t.Errorf("--limit 1 printed:\n%s\n--limit=1 exited %d and printed:\n%s\nwant the newest denial alone from both", spaced.Stdout, joined.Code, joined.Stdout)
	}
}
