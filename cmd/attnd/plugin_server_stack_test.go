package main_test

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func authorsServerPlugin(t *testing.T, id, tarball, gate string) string {
	t.Helper()
	dir := t.TempDir()
	for _, file := range []string{"attn-plugin.toml", "package.json", "src/index.ts"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "examples", "plugins", "sample-server", file))
		if err != nil {
			t.Fatal(err)
		}
		content := string(data)
		if file == "package.json" {
			content = strings.Replace(content, `"0.3.0"`, `"file:`+tarball+`"`, 1)
		}
		if file == "src/index.ts" {
			content = strings.Replace(content, `const harnessId = "sample-server"`, `const harnessId = "`+id+`"`, 1)
			content = strings.ReplaceAll(content, "SIGMA_ROOT_CONVERSATION", "CUSTOM_CONVERSATION")
			control := fmt.Sprintf(`const control = (await import("node:net")).createConnection(%q);
await new Promise<void>((resolve, reject) => { control.once("error", reject); control.once("data", () => resolve()); });
control.on("data", data => { if (String(data).trim() === "exit") process.exit(1); });
`, gate)
			content = strings.Replace(content, "await client.connect();", control+"await client.connect();\ncontrol.write('registered\\n');", 1)
		}
		path := filepath.Join(dir, file)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func serverInput(app *testworld.Peer, session, id, text string) protocol.SessionAnnotationsSubmitResultMessage {
	app.T.Helper()
	return testworld.Request(app, protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, SessionID: protocol.SessionID(session), RequestID: id, Text: text}, protocol.EventSessionAnnotationsSubmitResult, func(r protocol.SessionAnnotationsSubmitResultMessage) bool { return r.RequestID == id })
}

func TestAnInstalledServerPluginKeepsConversationOwnershipThroughRegistrationAndRestarts(t *testing.T) {
	for _, backend := range testworld.WorkerAndSharedHost {
		t.Run(string(backend), func(t *testing.T) {
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Sigma), testworld.OnPTYBackend(backend))
			gatePath := filepath.Join(s.Dir, "plugin-gate.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: gatePath, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			accept := func() net.Conn {
				t.Helper()
				_ = listener.SetDeadline(time.Now().Add(fakeagent.HangGuard))
				c, err := listener.Accept()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = c.Close() })
				return c
			}
			register := func(c net.Conn) {
				t.Helper()
				if _, err := c.Write([]byte("register\n")); err != nil {
					t.Fatal(err)
				}
				_ = c.SetReadDeadline(time.Now().Add(fakeagent.HangGuard))
				if line, err := bufio.NewReader(c).ReadString('\n'); err != nil || line != "registered\n" {
					t.Fatalf("registration: %q %v", line, err)
				}
			}
			s.Vars = append(s.Vars, "ATTN_SAMPLE_SERVER_EXECUTABLE="+filepath.Join(s.Dir, "bin", "sigma"), "ATTN_TEST_SERVER_CALLER_ENV=CUSTOM_CONVERSATION")
			s.Start()
			app := s.App()
			id := "installed_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
			plugin := authorsServerPlugin(t, id, packSDK(t), gatePath)
			installed := s.Attn("plugin", "install", "--path", plugin)
			if installed.Code != 0 {
				t.Fatalf("install: %s%s", installed.Stdout, installed.Stderr)
			}
			t.Logf("attn plugin install --path <clean example>: %s", strings.TrimSpace(installed.Stdout))
			control := accept()
			register(control)
			awaitAgentAvailable(app, fakeagent.Harness(id))
			s.Launch(testworld.Invocation{Binary: filepath.Join(s.Dir, "bin", "sigma"), Args: []string{"serve"}})
			server := s.AppServer(fakeagent.Sigma)
			session := s.Spawn(app, fakeagent.Harness(id), s.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.ExecutionTarget = protocol.Ptr("devvm") })
			view := s.Launched(session)
			conversation := view.ConversationID
			if got := server.Started(conversation).Target; got != "devvm" {
				t.Fatalf("target %q", got)
			}
			testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
			t.Logf("installed SDK example %s on %s, conversation %s; reused external server", id, backend, conversation)
			models := testworld.Request(app, protocol.HarnessModelsMessage{Cmd: protocol.CmdHarnessModels, Harness: id, RequestID: "models"}, protocol.EventHarnessModelsResult, func(r protocol.HarnessModelsResultMessage) bool { return r.RequestID == "models" })
			targets := testworld.Request(app, protocol.HarnessTargetsMessage{Cmd: protocol.CmdHarnessTargets, Harness: id, Cwd: s.Path("shop"), RequestID: "targets"}, protocol.EventHarnessTargetsResult, func(r protocol.HarnessTargetsResultMessage) bool { return r.RequestID == "targets" })
			if !models.Success || len(models.Models) == 0 || !targets.Success || len(targets.Targets) == 0 {
				t.Fatalf("discovery: models=%+v targets=%+v", models, targets)
			}
			t.Logf("catalog models=%d, execution targets=%+v", len(models.Models), targets.Targets)
			inputID := uuid.NewString()
			for range 2 {
				if r := serverInput(app, session, inputID, "the user's words"); !r.Success || r.Status != "delivered" {
					t.Fatalf("input: %+v", r)
				}
			}
			submission := server.Prompted(conversation)
			if submission.Author == "attn" || !strings.Contains(submission.RequestID, inputID) || server.Submissions(conversation) != 1 {
				t.Fatalf("user admission: %+v count=%d", submission, server.Submissions(conversation))
			}
			t.Logf("direct user admission: %+v; repeated input count=%d", submission, server.Submissions(conversation))
			server.Reply(conversation, "Done. <!-- attn:state=idle -->")
			testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
			peer := s.Spawn(app, fakeagent.Harness("shell"), s.Path("peer"))
			msg := s.Attn("agent", "msg", session, "review finished", "--source-session", peer)
			if msg.Code != 0 {
				t.Fatalf("notice: %s%s", msg.Stdout, msg.Stderr)
			}
			if got := server.Prompted(conversation); got.Author != "attn" {
				t.Fatalf("notice: %+v", got)
			}
			for _, binary := range []string{testworld.AttnBinary(t), filepath.Join(filepath.Dir(testworld.AttnBinary(t)), "attnd")} {
				got := strings.TrimSpace(server.RunTool(conversation, fmt.Sprintf("%q presence", binary)))
				t.Logf("%s presence from external server tool: %s", filepath.Base(binary), got)
				if got != "running inside attn (session "+session+")" {
					t.Fatalf("%s caller: %q", filepath.Base(binary), got)
				}
			}

			side := addProfile(t, app, "Side")
			sideApp := s.AppOn(side.ID)
			refused, _, _ := s.RequestSpawn(sideApp, fakeagent.Harness(id), s.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.ResumeSessionID = protocol.Ptr(conversation) })
			if refused.Success || !strings.Contains(protocol.Deref(refused.Error), app.SelectedProfile()) {
				t.Fatalf("cross-profile resume: %+v", refused)
			}
			terminalList := server.RunTool(conversation, fmt.Sprintf("%q terminal ls --all --json", testworld.AttnBinary(t)))
			if !strings.Contains(terminalList, session) {
				t.Fatalf("native Rust caller listing: %s", terminalList)
			}
			if removed := s.Attn("plugin", "uninstall", "sample-server"); removed.Code == 0 {
				t.Fatal("uninstalled active server owner")
			} else {
				t.Logf("attn plugin uninstall sample-server while active: code=%d %s%s", removed.Code, strings.TrimSpace(removed.Stdout), strings.TrimSpace(removed.Stderr))
			}
			if _, err := control.Write([]byte("exit\n")); err != nil {
				t.Fatal(err)
			}
			control = accept()
			if r := serverInput(app, session, uuid.NewString(), "while plugin restarts"); !r.Success || r.Status != "delivered" {
				t.Fatalf("plugin restart input: %+v", r)
			}
			server.Prompted(conversation)
			register(control)
			awaitAgentAvailable(app, fakeagent.Harness(id))
			s.Stop()
			s.Start()
			app = s.App()
			control = accept()
			if r := serverInput(app, session, uuid.NewString(), "before registration"); r.Status == "delivered" {
				t.Fatal("missing binding accepted input")
			}
			register(control)
			awaitAgentAvailable(app, fakeagent.Harness(id))
			if r := serverInput(app, session, uuid.NewString(), "after daemon restart"); !r.Success || r.Status != "delivered" {
				t.Fatalf("daemon restart input: %+v", r)
			}
			server.Prompted(conversation)
			reloaded := s.Attn("agent", "reload", session, "--source-session", session)
			if reloaded.Code != 0 {
				t.Fatalf("reload: %s%s", reloaded.Stdout, reloaded.Stderr)
			}
			again := s.Launched(session)
			if again.ConversationID != conversation {
				t.Fatalf("reload changed conversation to %s", again.ConversationID)
			}
			if target := server.Started(conversation).Target; target != "devvm" {
				t.Fatalf("reload changed target to %s", target)
			}
			closed := s.Attn("agent", "close", session, "-m", "ledger check", "--source-session", session)
			if closed.Code != 0 {
				t.Fatalf("close: %s%s", closed.Stdout, closed.Stderr)
			}
			resumed := testworld.Request(app, protocol.SessionReopenMessage{Cmd: protocol.CmdSessionReopen, SessionID: protocol.SessionID(session), RequestID: protocol.Ptr("resume-server")}, protocol.EventSessionReopenResult, func(r protocol.SessionReopenResultMessage) bool { return r.RequestID == "resume-server" })
			if !resumed.Success {
				t.Fatalf("ledger resume: %+v", resumed)
			}
			again = s.Launched(session)
			if again.ConversationID != conversation || server.Started(conversation).Target != "devvm" {
				t.Fatalf("ledger resume changed conversation/target: %+v", again)
			}

			t.Logf("reload and ledger resume kept native conversation %s and target devvm", conversation)
			terminal := s.Terminal(session)
			if err := s.Client().ObserveAgentConversation(protocol.TerminalID(terminal), "view-moved-on", ""); err != nil {
				t.Fatal(err)
			}
			testworld.AwaitSession(app, session, func(x protocol.Session) bool { return protocol.Deref(x.Hidden) })
			if removed := s.Attn("plugin", "uninstall", "sample-server"); removed.Code == 0 {
				t.Fatal("uninstalled zero-view server owner")
			}
			s.Stop()
			server.Reply(conversation, "A question while you were away? <!-- attn:state=waiting_input -->")
			recovery := s.PauseAt(pausepoint.DaemonStartupRecovery)
			s.StartHeldAt(recovery)
			control = accept()
			register(control)
			recovery.Release()
			app = s.App()
			hidden := testworld.AwaitSessionFromConnect(app, session, func(x protocol.Session) bool { return protocol.Deref(x.Hidden) })
			profile := hidden.ProfileID
			awaitAgentAvailable(app, fakeagent.Harness(id))
			settled := testworld.AwaitSession(app, session, func(x protocol.Session) bool {
				return protocol.Deref(x.Hidden) && x.State == protocol.SessionStateWaitingInput
			})
			if settled.ProfileID != profile {
				t.Fatal("recovery changed profile")
			}
			if r := serverInput(app, session, uuid.NewString(), "hidden followup"); !r.Success || r.Status != "delivered" {
				t.Fatalf("hidden input: %+v", r)
			}
			server.Prompted(conversation)
			if n := s.AppServers(fakeagent.Sigma); n != 1 {
				t.Fatalf("recovery started %d servers", n)
			}
			t.Logf("plugin restart, late registration after live-view and zero-view daemon restarts: same session %s and profile %s, one server, direct request IDs", session, profile)
		})
	}
}
