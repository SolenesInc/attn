package main_test

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyhost/ptyhosttest"
	"github.com/victorarias/attn/internal/testworld"
)

func TestOutpostUpdateRefusalsLeaveTheBinaryUntouched(t *testing.T) {
	for _, test := range []struct{ name, reason string }{
		{"unknown-channel", `channel "unknown"`},
		{"not-systemd", "not running under systemd"},
		{"shared-pty-host", "pty_shared_host_enabled"},
		{"staged-build", "staged build"},
		{"newer-schema", "contains migration"},
		{"before-updates", "install this build by hand once"},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := testworld.NewSSHAgent(t)
			remote, home := testworld.NewStack(t), testworld.NewStack(t)
			old := testworld.AttnBinaryAt(t, "before-update")
			if test.name == "newer-schema" || test.name == "before-updates" {
				old = testworld.AttnOutpostFixture(t, test.name)
			}
			remote.RunFrom(old)
			remote.SuperviseUpdates()
			if test.name == "shared-pty-host" {
				remote.Vars = append(remote.Vars, "ATTN_PTY_BACKEND=migrating", "ATTN_PTY_HOST_BINARY="+ptyhosttest.BuildWithSnapshotFormat(t, "outpost-update"))
			}
			if test.name == "not-systemd" {
				remote.Vars = append(remote.Vars, "INVOCATION_ID=")
			}
			next := testworld.AttnUpdateBinary(t, "after-update", "source", "")
			if test.name == "unknown-channel" {
				next = testworld.AttnBinaryAt(t, "after-update")
			}
			home.RunFrom(next)
			asset := next
			if test.name == "staged-build" {
				asset = old
			}
			home.Vars = append(home.Vars, "ATTN_HARNESS_OUTPOST_BINARY="+asset)
			for _, s := range []*testworld.Stack{remote, home} {
				a.Authorize(s.World)
				s.Start()
			}
			if test.name == "shared-pty-host" {
				requireStdout(t, remote.Attn("settings", "set", "pty_shared_host_enabled", "true"), "true")
			}
			address := bridgeAddress(t)
			requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
			app := enableOutposts(t, home)
			app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: address})
			row := outpostState(app, "outpost", protocol.OutpostStateRefused)
			if row.DaemonID == nil || row.HostKeyFingerprint == nil {
				t.Fatalf("first build refusal did not pin outpost: %+v", row)
			}
			binaryPath := filepath.Join(remote.Dir, "bin", "attn-daemon")
			before, err := os.ReadFile(binaryPath)
			if err != nil {
				t.Fatal(err)
			}
			app.Send(protocol.UpdateOutpostMessage{Cmd: protocol.CmdUpdateOutpost, ID: row.ID})
			result := testworld.Await[protocol.OutpostsUpdatedMessage](app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
				return len(m.Outposts) == 1 && m.Outposts[0].Update != nil && m.Outposts[0].Update.Phase == protocol.OutpostUpdatePhaseFailed
			})
			outcome := protocol.Deref(result.Outposts[0].Update.Outcome)
			if !strings.Contains(outcome, test.reason) {
				t.Fatalf("refusal %q omits %q", outcome, test.reason)
			}
			after, err := os.ReadFile(binaryPath)
			if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
				t.Fatalf("refused update replaced the binary: %v", err)
			}
			requireStdout(t, remote.Attn("settings", "get", "outpost_listen"), address)
			t.Logf("%s: %s; original binary and running outpost preserved", test.name, outcome)
		})
	}
}

func TestOutpostUpdateReturnsOnHomeBuildAndKeepsShell(t *testing.T) {
	for _, channel := range []string{"source", "release"} {
		t.Run(channel, func(t *testing.T) {
			if channel == "release" && runtime.GOOS != "linux" {
				t.Skip("release assets target Linux; the Linux stack run covers this strategy")
			}
			a := testworld.NewSSHAgent(t)
			remote, home := testworld.NewStack(t), testworld.NewStack(t)
			old := testworld.AttnBinaryAt(t, "before-update")
			remote.RunFrom(old)
			home.RunFrom(old)
			remote.SuperviseUpdates()
			asset := ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/vafter-update/attn-linux-"+runtime.GOARCH {
					http.NotFound(w, r)
					return
				}
				http.ServeFile(w, r, asset)
			}))
			defer server.Close()
			next := testworld.AttnUpdateBinary(t, "after-update", channel, server.URL)
			asset = next
			for _, s := range []*testworld.Stack{remote, home} {
				a.Authorize(s.World)
				s.Vars = append(s.Vars, "SHELL=/bin/bash")
				s.Start()
			}
			address := bridgeAddress(t)
			requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
			app := enableOutposts(t, home)
			app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: address})
			host := outpostState(app, "outpost", protocol.OutpostStateConnected).ID
			shell := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
			pidPath := filepath.Join(remote.Dir, "shell.pid")
			app.TypeLine(shell, "echo $$ > "+pidPath+"; echo survived-$((1+1))")
			app.AwaitScreen(shell, "survived-2")
			before, err := os.ReadFile(pidPath)
			if err != nil {
				t.Fatal(err)
			}
			home.Stop()
			home.RunFrom(next)
			home.Vars = append(home.Vars, "ATTN_HARNESS_OUTPOST_BINARY="+next)
			home.Start()
			app = home.App()
			outpostStateOnConnect(app, "outpost", protocol.OutpostStateRefused)
			app.Send(protocol.UpdateOutpostMessage{Cmd: protocol.CmdUpdateOutpost, ID: host})
			var done protocol.OutpostInfo
			testworld.Await[protocol.OutpostsUpdatedMessage](app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
				for _, row := range m.Outposts {
					if row.ID == host && row.Update != nil && (row.Update.Phase == protocol.OutpostUpdatePhaseDone || row.Update.Phase == protocol.OutpostUpdatePhaseFailed) {
						done = row
						return true
					}
				}
				return false
			})
			if done.Update.Phase != protocol.OutpostUpdatePhaseDone || done.State != protocol.OutpostStateConnected || !strings.Contains(protocol.Deref(done.Build), "after-update") {
				t.Fatalf("update outcome: %+v, update=%+v", done, done.Update)
			}
			app.AwaitScreen(shell, "survived-2")
			app.TypeLine(shell, "echo $$ > "+pidPath+"; echo still-$((2+2))")
			app.AwaitScreen(shell, "still-4")
			after, err := os.ReadFile(pidPath)
			if err != nil || string(before) != string(after) {
				t.Fatalf("shell PID changed: %s -> %s (%v)", before, after, err)
			}
			testworld.AwaitSession(app, shell, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
			t.Logf("%s update: connected on %s; shell pid=%s, screen and idle state survived", channel, protocol.Deref(done.Build), strings.TrimSpace(string(after)))
		})
	}
}
