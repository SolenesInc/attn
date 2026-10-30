package main_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyhost/ptyhosttest"
	"github.com/victorarias/attn/internal/testworld"
)

func awaitOutpostInstall(app *testworld.Peer) protocol.OutpostInfo {
	result := testworld.Await[protocol.OutpostsUpdatedMessage](app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		return len(m.Outposts) == 1 && m.Outposts[0].Update != nil && (m.Outposts[0].Update.Phase == protocol.OutpostUpdatePhaseDone || m.Outposts[0].Update.Phase == protocol.OutpostUpdatePhaseFailed)
	})
	return result.Outposts[0]
}

func TestSSHAddInstallsFromNothingAndUpdateRetriesFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "retry"}[fail], func(t *testing.T) {
			ssh := testworld.NewSSH(t)
			remote, home := testworld.NewStack(t), testworld.NewStack(t)
			ssh.Use(home.World)
			home.Vars = append(home.Vars, "ATTN_HARNESS_OUTPOST_BINARY="+testworld.AttnBinary(t))
			ssh.Route("fresh-outpost", remote)
			if err := os.Remove(ssh.InstallPath(remote)); err != nil {
				t.Fatal(err)
			}
			if fail {
				ssh.MachineCommand(remote, "chmod", "echo install-denied >&2; exit 1")
			}
			home.Start()
			app := enableOutposts(t, home)
			app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: "fresh-outpost"})
			row := awaitOutpostInstall(app)
			if fail {
				if row.Update.Phase != protocol.OutpostUpdatePhaseFailed || !strings.Contains(protocol.Deref(row.Update.Outcome), "install-denied") {
					t.Fatalf("first install: %+v", row.Update)
				}
				row = outpostState(app, "outpost", protocol.OutpostStateRefused)
				if !strings.Contains(protocol.Deref(row.LastError), "nothing installed") {
					t.Fatalf("missing install refusal: %+v", row)
				}
				ssh.MachineCommand(remote, "chmod", `exec /bin/chmod "$@"`)
				app.Send(protocol.UpdateOutpostMessage{Cmd: protocol.CmdUpdateOutpost, ID: row.ID})
				row = awaitOutpostInstall(app)
			}
			if row.Update.Phase != protocol.OutpostUpdatePhaseDone || row.State != protocol.OutpostStateConnected {
				t.Fatalf("install: %+v %+v", row, row.Update)
			}
			t.Logf("%s: connected from no binary; build=%s", t.Name(), protocol.Deref(row.Build))
		})
	}
}

func TestSSHUpdateKeepsShellAndReconnectNeverInstalls(t *testing.T) {
	ssh := testworld.NewSSH(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	old := testworld.AttnUpdateBinary(t, "before-update", "source", "")
	remote.RunFrom(old)
	home.RunFrom(old)
	ssh.Use(home.World)
	ssh.Route("update-outpost", remote)
	home.Start()
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: "update-outpost"})
	row := awaitOutpostInstall(app)
	if row.Update.Phase != protocol.OutpostUpdatePhaseDone {
		t.Fatal(row.Update)
	}
	host := row.ID
	shell := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	pidPath := filepath.Join(remote.Dir, "shell.pid")
	app.TypeLine(shell, "echo $$ > "+pidPath+"; echo survived-$((1+1))")
	app.AwaitScreen(shell, "survived-2")
	before, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	next := testworld.AttnUpdateBinary(t, "after-update", "source", "")
	home.Stop()
	home.RunFrom(next)
	home.Vars = append(home.Vars, "ATTN_HARNESS_OUTPOST_BINARY="+next)
	home.Start()
	app = home.App()
	row = outpostStateOnConnect(app, "outpost", protocol.OutpostStateRefused)
	installed, err := os.ReadFile(ssh.InstallPath(remote))
	if err != nil {
		t.Fatal(err)
	}
	oldBytes, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(installed) != sha256.Sum256(oldBytes) {
		t.Fatal("reconnect changed install path")
	}
	if !strings.Contains(protocol.Deref(row.LastError), "before-update") {
		t.Fatalf("refusal omits found build: %+v", row)
	}
	app.Send(protocol.UpdateOutpostMessage{Cmd: protocol.CmdUpdateOutpost, ID: host})
	row = awaitOutpostInstall(app)
	if row.Update.Phase != protocol.OutpostUpdatePhaseDone || row.State != protocol.OutpostStateConnected || !strings.Contains(protocol.Deref(row.Build), "after-update") {
		t.Fatalf("update: %+v %+v", row, row.Update)
	}
	previous, err := os.ReadFile(ssh.InstallPath(remote) + ".previous")
	if err != nil || sha256.Sum256(previous) != sha256.Sum256(oldBytes) {
		t.Fatalf("previous binary: %v", err)
	}
	app.TypeLine(shell, "echo $$ > "+pidPath+"; echo still-$((2+2))")
	app.AwaitScreen(shell, "still-4")
	after, err := os.ReadFile(pidPath)
	if err != nil || string(before) != string(after) {
		t.Fatalf("shell PID changed %s -> %s: %v", before, after, err)
	}
	t.Logf("Update connected on %s; shell PID %s survived", protocol.Deref(row.Build), strings.TrimSpace(string(after)))
	home.Stop()
	if err := os.Remove(ssh.InstallPath(remote)); err != nil {
		t.Fatal(err)
	}
	home.Start()
	app = home.App()
	row = outpostStateOnConnect(app, "outpost", protocol.OutpostStateRefused)
	if protocol.Deref(row.Build) != "nothing installed" {
		t.Fatalf("missing path kept stale build: %+v", row)
	}
	app.Send(protocol.UpdateOutpostMessage{Cmd: protocol.CmdUpdateOutpost, ID: host})
	row = awaitOutpostInstall(app)
	if row.Update.Phase != protocol.OutpostUpdatePhaseDone {
		t.Fatalf("reinstall: %+v", row.Update)
	}
	retained, err := os.ReadFile(ssh.InstallPath(remote) + ".previous")
	if err != nil || sha256.Sum256(retained) != sha256.Sum256(oldBytes) {
		t.Fatalf("reinstall lost previous build: %v", err)
	}
	t.Log("a missing install path replaced the stale build label; Update restored it and kept the existing rollback")

	app.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: host})
	testworld.Await[protocol.OutpostsUpdatedMessage](app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	if _, err := os.Stat(ssh.InstallPath(remote)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ssh.InstallPath(remote) + ".previous"); err != nil {
		t.Fatal(err)
	}
	conn, err := remote.DialUnix()
	if err == nil {
		conn.Close()
		t.Fatal("Remove left the remote daemon socket live")
	}
	t.Log("Remove stopped the remote instance and retained both binaries and its data")
}

func TestSSHInstallRefusalsKeepTheBinary(t *testing.T) {
	for _, tc := range []struct{ name, reason string }{
		{"platform", "Linux amd64 or arm64"}, {"logind", "KillUserProcesses is on"}, {"staged-build", "staged build"}, {"newer-schema", "contains migration"}, {"shared-pty-host", "pty_shared_host_enabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ssh := testworld.NewSSH(t)
			remote, home := testworld.NewStack(t), testworld.NewStack(t)
			old := testworld.AttnUpdateBinary(t, "before-update", "source", "")
			if tc.name == "newer-schema" {
				old = testworld.AttnOutpostFixture(t, "newer-schema")
			}
			remote.RunFrom(old)
			if tc.name == "shared-pty-host" {
				remote.Vars = append(remote.Vars, "ATTN_PTY_BACKEND=migrating", "ATTN_PTY_HOST_BINARY="+ptyhosttest.BuildWithSnapshotFormat(t, "outpost-install"))
				remote.Start()
				requireStdout(t, remote.Attn("settings", "set", "pty_shared_host_enabled", "true"), "true")
			} else if tc.name == "newer-schema" {
				remote.Start()
			}
			ssh.Use(home.World)
			ssh.Route("refused-outpost", remote)
			asset := testworld.AttnBinary(t)
			if tc.name == "staged-build" {
				asset = old
			}
			home.Vars = append(home.Vars, "ATTN_HARNESS_OUTPOST_BINARY="+asset)
			if tc.name == "platform" {
				ssh.MachineCommand(remote, "uname", "echo Darwin arm64")
			}
			if tc.name == "logind" {
				ssh.MachineCommand(remote, "busctl", "echo b true")
			}
			before, err := os.ReadFile(ssh.InstallPath(remote))
			if err != nil {
				t.Fatal(err)
			}
			home.Start()
			app := enableOutposts(t, home)
			app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: "refused-outpost"})
			row := awaitOutpostInstall(app)
			if row.Update.Phase != protocol.OutpostUpdatePhaseFailed || !strings.Contains(protocol.Deref(row.Update.Outcome), tc.reason) {
				t.Fatalf("Add refusal: %+v", row.Update)
			}
			app.Send(protocol.UpdateOutpostMessage{Cmd: protocol.CmdUpdateOutpost, ID: row.ID})
			row = awaitOutpostInstall(app)
			if row.Update.Phase != protocol.OutpostUpdatePhaseFailed || !strings.Contains(protocol.Deref(row.Update.Outcome), tc.reason) {
				t.Fatalf("Update refusal: %+v", row.Update)
			}
			after, err := os.ReadFile(ssh.InstallPath(remote))
			if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
				t.Fatalf("refusal changed binary: %v", err)
			}
			t.Log(protocol.Deref(row.Update.Outcome))
		})
	}
}

func TestSSHStagedInstallRefusesANamedInstanceAtDefaultPath(t *testing.T) {
	s := testworld.NewStack(t)
	binary := testworld.AttnBinary(t)
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(s.Dir, "install")
	target := filepath.Join(root, ".local", "bin", "attn")
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("preserve default"), 0700); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(filepath.Dir(target), ".attn-install-test")
	if err := os.WriteFile(staged, data, 0700); err != nil {
		t.Fatal(err)
	}
	build := s.Run(testworld.Invocation{Args: []string{"--build-info-json"}})
	var info struct {
		Version     string `json:"version"`
		Fingerprint string `json:"sourceFingerprint"`
		Protocol    string `json:"protocolVersion"`
	}
	if err := json.Unmarshal([]byte(build.Stdout), &info); err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(bridge.Build{Version: info.Version, SourceFingerprint: info.Fingerprint, ProtocolVersion: info.Protocol})
	result := s.Run(testworld.Invocation{Binary: staged, Args: []string{"_outpost-install", "--size", fmt.Sprint(len(data)), "--sha256", fmt.Sprintf("%x", sha256.Sum256(data)), "--build", string(expected), "--target", target}, Env: []string{"ATTN_INSTANCE=install-check", "ATTN_HARNESS_OUTPOST_INSTALL_ROOT=" + root}})
	if result.Code == 0 || !strings.Contains(result.Stderr, "refuses default install path") {
		t.Fatalf("named install refusal: %+v", result)
	}
	after, err := os.ReadFile(target)
	if err != nil || string(after) != "preserve default" {
		t.Fatalf("default changed: %q %v", after, err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("failed staged binary left behind: %v", err)
	}
	t.Log(strings.TrimSpace(result.Stderr))
}

func TestSSHRemoveForgetsAfterRemoteStopFailure(t *testing.T) {
	ssh := testworld.NewSSH(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	ssh.Use(home.World)
	remote.Start()
	ssh.Route("stop-failure", remote)
	home.Start()
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: "stop-failure"})
	row := awaitOutpostInstall(app)
	if row.Update.Phase != protocol.OutpostUpdatePhaseDone {
		t.Fatal(row.Update)
	}
	if err := os.Chmod(ssh.InstallPath(remote), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(ssh.InstallPath(remote), 0700); err != nil {
			t.Error(err)
		}
	})
	app.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: row.ID})
	testworld.Await[protocol.OutpostsUpdatedMessage](app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	warning := testworld.Await[protocol.CommandErrorMessage](app, protocol.EventCommandError, func(m protocol.CommandErrorMessage) bool { return protocol.Deref(m.Cmd) == protocol.CmdRemoveOutpost })
	if !strings.Contains(warning.Error, "may still be running") || !strings.Contains(warning.Error, "enrollment leave") || !strings.Contains(warning.Error, "daemon stop") {
		t.Fatalf("missing remote-stop warning: %+v", warning)
	}
	t.Log(warning.Error)
	if rows := home.App().Initial.Outposts; len(rows) != 0 {
		t.Fatalf("removed row returned: %+v", rows)
	}
}
