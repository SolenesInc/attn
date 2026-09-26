package main_test

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const browserHostToken = "browser-host-secret"

func connectBrowserHost(t *testing.T, s *testworld.Stack) *testworld.Peer {
	t.Helper()
	token, err := os.ReadFile(filepath.Join(s.Dir, config.ClientTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	host := s.Connect(protocol.ClientHelloMessage{
		Cmd:              protocol.CmdClientHello,
		ClientKind:       "tauri-app",
		Version:          "protocol-" + protocol.ProtocolVersion,
		Capabilities:     []string{protocol.CapabilityWorkspaceSessions, protocol.CapabilityBinaryPtyOutput, protocol.CapabilityBrowserHost},
		ClientToken:      protocol.Ptr(strings.TrimSpace(string(token))),
		BrowserHostToken: protocol.Ptr(browserHostToken),
	}, http.Header{"Origin": []string{"tauri://localhost"}})
	testworld.Await[protocol.InitialStateMessage](host, protocol.EventInitialState, nil)
	return host
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func refused(t *testing.T, got testworld.Result, code int, want string) {
	t.Helper()
	if got.Code != code || !strings.Contains(got.Stderr, want) || got.Stdout != "" {
		t.Errorf("exited %d with stdout %q and stderr:\n%s\nwant %d and %q", got.Code, got.Stdout, got.Stderr, code, want)
	}
}

func TestTheCommandsAnAgentRunsFromItsSessionActOnThatSession(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Vars = append(s.Vars, "ATTN_BROWSER_HOST_TOKEN="+browserHostToken)
	s.Start()
	app := s.App()
	session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	other := s.Spawn(app, fakeagent.Claude, s.Path("blog"))
	s.Launched(session)
	s.Launched(other)
	inSession := func(args ...string) testworld.Result {
		t.Helper()
		return s.Run(testworld.Invocation{Args: args, Session: session})
	}

	t.Run("session rename", func(t *testing.T) {
		if got := inSession("session", "rename", "pi resume support"); got.Code != 0 {
			t.Fatalf("rename exited %d: %s", got.Code, got.Stderr)
		}
		testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.Label == "pi resume support" })
		if got := inSession("session", "rename", "--session", other, "  review store tripwires "); got.Code != 0 {
			t.Fatalf("rename --session exited %d: %s", got.Code, got.Stderr)
		}
		testworld.AwaitSession(app, other, func(x protocol.Session) bool { return x.Label == "review store tripwires" })
		if got := inSession("session", "rename", "checkout", "--session", other); got.Code != 0 {
			t.Fatalf("rename with --session after the name exited %d: %s", got.Code, got.Stderr)
		}
		testworld.AwaitSession(app, other, func(x protocol.Session) bool { return x.Label == "checkout" })

		refused(t, inSession("session", "rename", "pi", "resume"), 2, "exactly one name is required")
		refused(t, inSession("session", "rename", "   "), 2, "the name cannot be empty")
		refused(t, s.Attn("session", "rename", "orphan"), 2, "--session")
	})

	t.Run("journal append", func(t *testing.T) {
		entry := filepath.Join(s.Dir, "entry.md")
		writeFile(t, entry, "  shipped the parser  \n", 0o644)
		var appended struct {
			RelPath string `json:"rel_path"`
			Hash    string `json:"hash"`
		}
		inSession("journal", "append", "--entry-file", entry, "--date", "2026-07-05", "--json").JSON(t, &appended)
		if appended.RelPath != "journal/2026-07-05.md" || appended.Hash == "" {
			t.Errorf("journal append --json printed %+v, want journal/2026-07-05.md and its hash", appended)
		}
		if got := inSession("journal", "append", "--entry", "reviewed the store", "--date", "2026-07-05"); strings.TrimSpace(got.Stdout) != "appended to journal/2026-07-05.md" {
			t.Errorf("journal append printed %q", got.Stdout)
		}
		journal, err := os.ReadFile(filepath.Join(s.Dir, "notebook", "journal", "2026-07-05.md"))
		if err != nil {
			t.Fatal(err)
		}
		shipped := strings.Index(string(journal), "shipped the parser\n")
		reviewed := strings.Index(string(journal), "reviewed the store")
		if shipped < 0 || reviewed < shipped || strings.Contains(string(journal), "  shipped") {
			t.Errorf("journal holds:\n%s\nwant the trimmed file entry, then the inline one", journal)
		}

		refused(t, inSession("journal", "append", "--entry", "inline", "--entry-file", entry), 2, "only one of --entry or --entry-file")
		refused(t, inSession("journal", "append"), 2, "--entry or --entry-file is required")
	})

	t.Run("ticket show", func(t *testing.T) {
		if _, err := s.Client().CreateTicket(session, "Price the order", "", "pricing"); err != nil {
			t.Fatal(err)
		}
		var ticket struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}
		inSession("ticket", "show", "pricing", "--session", other, "--json").JSON(t, &ticket)
		if ticket.ID != "pricing" || ticket.Title != "Price the order" {
			t.Errorf("ticket show --json printed %+v", ticket)
		}
		shown := inSession("ticket", "show", "--session", other, "pricing")
		if lines := strings.Split(shown.Stdout, "\n"); !strings.HasPrefix(lines[0], "pricing\t") || len(lines) < 2 || lines[1] != "Price the order" {
			t.Errorf("ticket show with --session first printed:\n%s", shown.Stdout)
		}

		refused(t, inSession("ticket", "show"), 2, "expected exactly one ticket id")
		refused(t, inSession("ticket", "show", "pricing", "extra"), 2, "expected exactly one ticket id")
		refused(t, inSession("ticket", "show", "pricing", "--bogus"), 2, "-bogus")
	})

	t.Run("open", func(t *testing.T) {
		readme := s.Path("shop", "README.md")
		notes := s.Path("shop", "s-notes.md")
		writeFile(t, readme, "# shop\n", 0o644)
		writeFile(t, notes, "# notes\n", 0o644)
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		relativeNotes, err := filepath.Rel(cwd, notes)
		if err != nil {
			t.Fatal(err)
		}
		planted, err := s.Client().SeedPlant("", "Add a discount field", "Checkout needs a field for discount codes.", "", "", "")
		if err != nil {
			t.Fatal(err)
		}

		for _, tc := range []struct {
			args      []string
			workspace string
			tile      string
		}{
			{args: []string{readme}, workspace: "workspace-shop", tile: readme},
			{args: []string{"--session", other, readme}, workspace: "workspace-blog", tile: readme},
			{args: []string{relativeNotes, "--session=" + other}, workspace: "workspace-blog", tile: notes},
			{args: []string{planted.Seed.ID, "--session", other}, workspace: "workspace-blog", tile: planted.Seed.ID},
		} {
			got := s.Run(testworld.Invocation{Args: append([]string{"open"}, tc.args...), Session: session})
			if got.Code != 0 || !strings.HasPrefix(got.Stdout, "opened ") {
				t.Errorf("attn open %q exited %d: %s%s", tc.args, got.Code, got.Stdout, got.Stderr)
				continue
			}
			testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(m protocol.WorkspaceLayoutUpdatedMessage) bool {
				return m.WorkspaceLayout.WorkspaceID == tc.workspace && strings.Contains(m.WorkspaceLayout.LayoutJson, tc.tile)
			})
		}

		refused(t, inSession("open", "s-not-valid"), 1, `"s-not-valid" is not a seed id`)
		refused(t, inSession("open", "--session", session), 1, "usage: attn open")
		refused(t, inSession("open"), 1, "usage: attn open")
		refused(t, inSession("open", "a.md", "b.md"), 1, "unexpected extra arguments")
		refused(t, inSession("open", readme, "--nope"), 1, "usage: attn open")
	})

	t.Run("browser captures", func(t *testing.T) {
		host := connectBrowserHost(t, s)
		if got := inSession("browser", "open", "https://example.invalid/"); got.Code != 0 {
			t.Fatalf("browser open exited %d: %s", got.Code, got.Stderr)
		}
		for _, tc := range []struct {
			command, action, file string
		}{
			{command: "screenshot", action: "screenshot", file: "capture.png"},
			{command: "pdf", action: "print_page", file: "capture.pdf"},
		} {
			path := filepath.Join(s.Dir, tc.file)
			writeFile(t, path, "public", 0o644)
			ran := make(chan testworld.Result, 1)
			go func() { ran <- inSession("browser", tc.command, path) }()
			request := testworld.Await(host, protocol.EventBrowserControlRequest, func(r protocol.BrowserControlRequestMessage) bool { return r.Action == tc.action })
			host.Send(protocol.BrowserControlResultMessage{
				Cmd:       protocol.CmdBrowserControlResult,
				RequestID: request.RequestID,
				Success:   true,
				Data:      protocol.Ptr(base64.StdEncoding.EncodeToString([]byte("private " + tc.command))),
			})
			if got := <-ran; got.Code != 0 || strings.TrimSpace(got.Stdout) != path {
				t.Errorf("browser %s exited %d printing %q: %s", tc.command, got.Code, got.Stdout, got.Stderr)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if content, _ := os.ReadFile(path); info.Mode().Perm() != 0o600 || string(content) != "private "+tc.command {
				t.Errorf("browser %s left %s as %o holding %q, want 600 holding the capture", tc.command, path, info.Mode().Perm(), content)
			}
		}
	})
}
