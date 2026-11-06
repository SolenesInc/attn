package main_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/ptyhost/ptyhosttest"
	"github.com/victorarias/attn/internal/store"
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
			home.InstallsOnOutposts(testworld.AttnBinary(t), "")
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
				if row.State != protocol.OutpostStateRefused || !strings.Contains(protocol.Deref(row.LastError), "nothing installed") {
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
	home.InstallsOnOutposts(next, "")
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

const terminalOwner = `sh -c 'basename "$(ps -o comm= -p $(ps -o ppid= -p $PPID))"'`

func sha256OfFile(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func TestAnUpdateCarriesTheHostToAnOutpostAndATerminalInTheHostSurvivesIt(t *testing.T) {
	const beforeFormat, afterFormat = "outpost-before", "outpost-after"
	before := testworld.AttnUpdateBinaryWithSnapshotFormat(t, "before-update", beforeFormat)
	after := testworld.AttnUpdateBinaryWithSnapshotFormat(t, "after-update", afterFormat)
	hostBefore := ptyhosttest.BuildWithSnapshotFormat(t, beforeFormat)
	hostAfter := ptyhosttest.BuildWithSnapshotFormat(t, afterFormat)

	ssh := testworld.NewSSH(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	remote.Vars = append(remote.Vars, "ATTN_PTY_BACKEND=", "ATTN_PTY_SKIP_STARTUP_PROBE=")
	home.RunFrom(before)
	home.InstallsOnOutposts(before, hostBefore)
	ssh.Use(home.World)
	ssh.Route("host-outpost", remote)
	if err := os.Remove(ssh.InstallPath(remote)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			remote.LogDaemonTail()
		}
	})
	home.Start()
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: "host-outpost"})
	row := awaitOutpostInstall(app)
	if row.Update.Phase != protocol.OutpostUpdatePhaseDone || row.State != protocol.OutpostStateConnected {
		t.Fatalf("Add: %+v %+v", row, row.Update)
	}
	installedHost := filepath.Join(filepath.Dir(ssh.InstallPath(remote)), ptyhost.BinaryName)
	if sha256OfFile(t, installedHost) != sha256OfFile(t, hostBefore) {
		t.Fatal("Add did not install the home's host beside attn")
	}
	remote.RunFrom(ssh.InstallPath(remote))
	requireStdout(t, remote.Attn("settings", "set", "pty_shared_host_enabled", "true"), "true")

	host := row.ID
	shell := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	pidPath := filepath.Join(remote.Dir, "shell.pid")
	app.TypeLine(shell, `echo $$ > `+pidPath+`; echo "owner=$(`+terminalOwner+`)"`)
	app.AwaitScreen(shell, "owner="+ptyhost.BinaryName)
	shellBefore, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	if format := attachedFormat(remote.App(), app.Terminal(shell)); format != beforeFormat {
		t.Fatalf("terminal format before the update = %q, want %q", format, beforeFormat)
	}

	home.Stop()
	home.RunFrom(after)
	home.InstallsOnOutposts(after, hostAfter)
	home.Start()
	app = home.App()
	row = outpostStateOnConnect(app, "outpost", protocol.OutpostStateRefused)
	app.Send(protocol.UpdateOutpostMessage{Cmd: protocol.CmdUpdateOutpost, ID: host})
	row = awaitOutpostInstall(app)
	if row.Update.Phase != protocol.OutpostUpdatePhaseDone || row.State != protocol.OutpostStateConnected || !strings.Contains(protocol.Deref(row.Build), "after-update") {
		t.Fatalf("Update: %+v %+v", row, row.Update)
	}
	if sha256OfFile(t, installedHost) != sha256OfFile(t, hostAfter) {
		t.Fatal("Update did not replace the host beside attn")
	}
	if sha256OfFile(t, installedHost+".previous") != sha256OfFile(t, hostBefore) {
		t.Fatal("Update did not keep the previous host")
	}
	remoteApp := remote.App()
	if came := initialSession(t, remoteApp, shell); protocol.Deref(came.TerminalBuildStale) {
		t.Error("the host terminal came back with the reload notice")
	}
	if format := attachedFormat(remoteApp, app.Terminal(shell)); format != afterFormat {
		t.Errorf("terminal format after the update = %q, want the new build's %q", format, afterFormat)
	}
	app.TypeLine(shell, `echo $$ > `+pidPath+`; echo "still=$(`+terminalOwner+`)-$((2+2))"`)
	app.AwaitScreen(shell, "still="+ptyhost.BinaryName+"-4")
	shellAfter, err := os.ReadFile(pidPath)
	if err != nil || string(shellBefore) != string(shellAfter) {
		t.Fatalf("shell PID changed %s -> %s: %v", shellBefore, shellAfter, err)
	}
	fresh := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("fresh"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	app.TypeLine(fresh, `echo "fresh=$(`+terminalOwner+`)"`)
	app.AwaitScreen(fresh, "fresh="+ptyhost.BinaryName)
	t.Logf("shell PID %s kept its host across %s -> %s, and a new terminal started in the host", strings.TrimSpace(string(shellAfter)), beforeFormat, afterFormat)
}

func outpostWithMatchingBuild(t *testing.T) (*testworld.SSH, *testworld.Stack, *testworld.Stack) {
	t.Helper()
	const format = "outpost-matching"
	build := testworld.AttnUpdateBinaryWithSnapshotFormat(t, "matching", format)
	ssh := testworld.NewSSH(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	remote.Vars = append(remote.Vars, "ATTN_PTY_BACKEND=", "ATTN_PTY_SKIP_STARTUP_PROBE=")
	remote.RunFrom(build)
	home.RunFrom(build)
	home.InstallsOnOutposts(build, ptyhosttest.BuildWithSnapshotFormat(t, format))
	ssh.Use(home.World)
	ssh.Route("matching-outpost", remote)
	remote.RunFrom(ssh.InstallPath(remote))
	return ssh, remote, home
}

func awaitTerminalOwner(app *testworld.Peer, shell, label, owner string) {
	app.TypeLine(shell, `echo "`+label+`=$(`+terminalOwner+`)"`)
	app.AwaitScreen(shell, label+"="+owner)
}

func TestAddInstallsTheMissingHostOnAnOutpostAtTheHomeBuild(t *testing.T) {
	ssh, remote, home := outpostWithMatchingBuild(t)
	if err := os.Remove(ssh.HostPath(remote)); err != nil {
		t.Fatal(err)
	}
	home.Start()
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: "matching-outpost"})
	row := awaitOutpostInstall(app)
	if row.Update.Phase != protocol.OutpostUpdatePhaseDone || row.State != protocol.OutpostStateConnected || protocol.Deref(row.HostMissing) {
		t.Fatalf("Add: %+v %+v", row, row.Update)
	}
	if _, err := os.Stat(ssh.HostPath(remote)); err != nil {
		t.Fatalf("Add left the outpost without a host: %v", err)
	}
	requireStdout(t, remote.Attn("settings", "set", "pty_shared_host_enabled", "true"), "true")
	host := row.ID
	shell := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	awaitTerminalOwner(app, shell, "owner", ptyhost.BinaryName)
}

func TestAPairedOutpostWithoutTheHostKeepsWorkersAndUpdateInstallsIt(t *testing.T) {
	ssh, remote, home := outpostWithMatchingBuild(t)
	home.Start()
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: "matching-outpost"})
	row := awaitOutpostInstall(app)
	if row.Update.Phase != protocol.OutpostUpdatePhaseDone || protocol.Deref(row.HostMissing) {
		t.Fatalf("Add with a host beside attn: %+v %+v", row, row.Update)
	}
	home.Stop()
	remote.Attn("daemon", "stop")
	if err := os.Remove(ssh.HostPath(remote)); err != nil {
		t.Fatal(err)
	}
	home.Start()
	app = home.App()
	row = testworld.AwaitSince(app, 0, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		return len(m.Outposts) == 1 && m.Outposts[0].State == protocol.OutpostStateConnected && protocol.Deref(m.Outposts[0].HostMissing)
	}).Outposts[0]
	if _, err := os.Stat(ssh.HostPath(remote)); !os.IsNotExist(err) {
		t.Fatalf("a reconnect installed the host: %v", err)
	}

	host := row.ID
	shell := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	pidPath := filepath.Join(remote.Dir, "shell.pid")
	app.TypeLine(shell, `echo $$ > `+pidPath+`; echo "owner=$(`+terminalOwner+`)"`)
	app.AwaitScreen(shell, "owner=attn")
	before, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}

	app.Send(protocol.UpdateOutpostMessage{Cmd: protocol.CmdUpdateOutpost, ID: host})
	row = awaitOutpostInstall(app)
	if row.Update.Phase != protocol.OutpostUpdatePhaseDone || row.State != protocol.OutpostStateConnected || protocol.Deref(row.HostMissing) {
		t.Fatalf("Update: %+v %+v", row, row.Update)
	}
	app = home.App()
	app.TypeLine(shell, `echo $$ > `+pidPath+`; echo "still=$(`+terminalOwner+`)"`)
	app.AwaitScreen(shell, "still=attn")
	after, err := os.ReadFile(pidPath)
	if err != nil || string(before) != string(after) {
		t.Fatalf("shell PID changed %s -> %s: %v", before, after, err)
	}
	requireStdout(t, remote.Attn("settings", "set", "pty_shared_host_enabled", "true"), "true")
	fresh := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("fresh"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	awaitTerminalOwner(app, fresh, "fresh", ptyhost.BinaryName)
	t.Logf("shell PID %s stayed on its worker; a new terminal started in the installed host", strings.TrimSpace(string(after)))
}

func TestAReleaseHomeDownloadsTheHostWithAttnAndInstallsNothingWithoutIt(t *testing.T) {
	var hostBytes []byte
	assets := http.NewServeMux()
	var homeBinary string
	assets.HandleFunc("/v9.9.9/", func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		switch {
		case strings.HasPrefix(name, "attn-linux-"):
			http.ServeFile(w, r, homeBinary)
		case strings.HasPrefix(name, ptyhost.BinaryName+"-linux-") && hostBytes != nil:
			w.Write(hostBytes)
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewServer(assets)
	t.Cleanup(server.Close)
	homeBinary = testworld.AttnUpdateBinary(t, "9.9.9", "release", server.URL)

	ssh := testworld.NewSSH(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	home.RunFrom(homeBinary)
	ssh.Use(home.World)
	ssh.Route("release-outpost", remote)
	if err := os.Remove(ssh.InstallPath(remote)); err != nil {
		t.Fatal(err)
	}
	home.Start()
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: "release-outpost"})
	row := awaitOutpostInstall(app)
	outcome := protocol.Deref(row.Update.Outcome)
	if row.Update.Phase != protocol.OutpostUpdatePhaseFailed || !strings.Contains(outcome, ptyhost.BinaryName+"-linux-") || !strings.Contains(outcome, "404") {
		t.Fatalf("Add without a host asset: %+v", row.Update)
	}
	if _, err := os.Stat(ssh.InstallPath(remote)); !os.IsNotExist(err) {
		t.Fatalf("a failed host download installed attn: %v", err)
	}
	t.Log(outcome)

	hostBytes = []byte("the released host")
	app.Send(protocol.UpdateOutpostMessage{Cmd: protocol.CmdUpdateOutpost, ID: row.ID})
	row = awaitOutpostInstall(app)
	if row.Update.Phase != protocol.OutpostUpdatePhaseDone || row.State != protocol.OutpostStateConnected {
		t.Fatalf("Update with both assets: %+v %+v", row, row.Update)
	}
	installed, err := os.ReadFile(filepath.Join(filepath.Dir(ssh.InstallPath(remote)), ptyhost.BinaryName))
	if err != nil || string(installed) != string(hostBytes) {
		t.Fatalf("installed host %q: %v", installed, err)
	}
}

func TestASourceHomeWithoutRustNamesTheToolchainAndInstallsNothing(t *testing.T) {
	ssh := testworld.NewSSH(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	home.RunFrom(testworld.AttnUpdateBinary(t, "no-rust", "source", ""))
	home.Vars = append(home.Vars, "PATH="+t.TempDir())
	ssh.Use(home.World)
	ssh.Route("rustless-outpost", remote)
	if err := os.Remove(ssh.InstallPath(remote)); err != nil {
		t.Fatal(err)
	}
	home.Start()
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: "rustless-outpost"})
	row := awaitOutpostInstall(app)
	outcome := protocol.Deref(row.Update.Outcome)
	if row.Update.Phase != protocol.OutpostUpdatePhaseFailed || !strings.Contains(outcome, "Rust toolchain") || !strings.Contains(outcome, "rustup target add") {
		t.Fatalf("Add without cargo: %+v", row.Update)
	}
	if _, err := os.Stat(ssh.InstallPath(remote)); !os.IsNotExist(err) {
		t.Fatalf("a failed host build installed attn: %v", err)
	}
	t.Log(outcome)
}

func recordMigrationFromANewerBuild(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))`, store.LatestSchemaVersion()+1); err != nil {
		t.Fatal(err)
	}
}

func TestSSHInstallRefusalsKeepTheBinary(t *testing.T) {
	for _, tc := range []struct{ name, reason string }{
		{"platform", "Linux amd64 or arm64"}, {"newer-schema", "contains migration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ssh := testworld.NewSSH(t)
			remote, home := testworld.NewStack(t), testworld.NewStack(t)
			remote.RunFrom(testworld.AttnUpdateBinary(t, "before-update", "source", ""))
			if tc.name == "newer-schema" {
				remote.Start()
				remote.Stop()
				recordMigrationFromANewerBuild(t, filepath.Join(remote.Dir, "attn.db"))
			}
			ssh.Use(home.World)
			ssh.Route("refused-outpost", remote)
			home.InstallsOnOutposts(testworld.AttnBinary(t), "")
			if tc.name == "platform" {
				ssh.MachineCommand(remote, "uname", "echo Darwin arm64")
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

func TestSSHStagedInstallRefusalsKeepTheInstalledBinary(t *testing.T) {
	for _, tc := range []struct{ name, reason string }{
		{"named-instance-at-default-path", "refuses default install path"},
		{"staged-build", "staged build"},
		{"staged-host", ptyhost.BinaryName},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			home := bridge.Build{Version: info.Version, SourceFingerprint: info.Fingerprint, ProtocolVersion: info.Protocol}
			env := []string{"ATTN_HARNESS_OUTPOST_INSTALL_ROOT=" + root}
			stagedHost := filepath.Join(filepath.Dir(target), ".attn-pty-host-install")
			hostBytes := []byte("the host of this build")
			hostDigest := sha256.Sum256(hostBytes)
			if err := os.WriteFile(stagedHost, hostBytes, 0o700); err != nil {
				t.Fatal(err)
			}
			switch tc.name {
			case "named-instance-at-default-path":
				env = append(env, "ATTN_INSTANCE=install-check")
			case "staged-build":
				home.Version = "home-" + home.Version
			case "staged-host":
				hostDigest = sha256.Sum256([]byte("what the home sent"))
			}
			expected, _ := json.Marshal(home)
			result := s.Run(testworld.Invocation{Binary: staged, Args: []string{"_outpost-install", "--size", fmt.Sprint(len(data)), "--sha256", fmt.Sprintf("%x", sha256.Sum256(data)), "--host", stagedHost, "--host-size", fmt.Sprint(len(hostBytes)), "--host-sha256", fmt.Sprintf("%x", hostDigest), "--build", string(expected), "--target", target}, Env: env})
			if result.Code == 0 || !strings.Contains(result.Stderr, tc.reason) {
				t.Fatalf("install refusal: %+v", result)
			}
			after, err := os.ReadFile(target)
			if err != nil || string(after) != "preserve default" {
				t.Fatalf("installed binary changed: %q %v", after, err)
			}
			if _, err := os.Stat(staged); !os.IsNotExist(err) {
				t.Fatalf("failed staged binary left behind: %v", err)
			}
			if _, err := os.Stat(stagedHost); !os.IsNotExist(err) {
				t.Fatalf("failed staged host left behind: %v", err)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(target), ptyhost.BinaryName)); !os.IsNotExist(err) {
				t.Fatalf("refused install placed a host: %v", err)
			}
			t.Log(strings.TrimSpace(result.Stderr))
		})
	}
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

func TestSSHUnpairedAddFinishesAfterAuthentication(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprintf("restart=%v", restart), func(t *testing.T) {
			ssh := testworld.NewSSH(t)
			remote, home := testworld.NewStack(t), testworld.NewStack(t)
			ssh.Use(home.World)
			ssh.Route("first-auth", remote)
			if err := os.Remove(ssh.InstallPath(remote)); err != nil {
				t.Fatal(err)
			}
			home.InstallsOnOutposts(testworld.AttnBinary(t), "")
			ssh.Fail("first-auth", "Host key verification failed.")
			home.Start()
			app := enableOutposts(t, home)
			app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: "first-auth"})
			refused := outpostState(app, "outpost", protocol.OutpostStateRefused)
			if refused.HostKeyFingerprint != nil || refused.DaemonID != nil {
				t.Fatalf("failed ssh pinned a row: %+v", refused)
			}
			if restart {
				home.Stop()
			} else {
				home.AwaitTimer(bridge.RetryTimer)
			}
			ssh.Restore("first-auth")
			ssh.StartMaster(home, refused.ID, "first-auth")
			if restart {
				home.Start()
				app = home.App()
			}
			if !restart {
				home.Advance(bridge.RefusedRetryInterval)
			}
			result := testworld.AwaitSince(app, 0, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
				return len(m.Outposts) == 1 && (m.Outposts[0].State == protocol.OutpostStateConnected || m.Outposts[0].State == protocol.OutpostStateRefused && protocol.Deref(m.Outposts[0].LastError) != protocol.Deref(refused.LastError))
			}).Outposts[0]
			if result.State != protocol.OutpostStateConnected {
				t.Fatalf("answering ssh did not finish Add: %+v", result)
			}
			if _, err := os.Stat(ssh.InstallPath(remote)); err != nil {
				t.Fatal(err)
			}
			t.Logf("restart=%v: ssh refusal -> master created out of band -> installed and paired, no Update", restart)
		})
	}
}

func TestSSHDisablingOutpostsCancelsUnpairedAdd(t *testing.T) {
	ssh := testworld.NewSSH(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	ssh.Use(home.World)
	ssh.Route("held-add", remote)
	if err := os.Remove(ssh.InstallPath(remote)); err != nil {
		t.Fatal(err)
	}
	home.InstallsOnOutposts(testworld.AttnBinary(t), "")
	release := ssh.Pause("held-add")
	defer release()
	home.Start()
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: "held-add"})
	ssh.AwaitPausedRequest("held-add")
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"), "false")
	release()
	ssh.Drain("held-add")
	if _, err := os.Stat(ssh.InstallPath(remote)); !os.IsNotExist(err) {
		t.Fatalf("disabled Add installed a binary: %v", err)
	}
	t.Log("Outposts off cancelled the pending unpaired Add; the external requests drained and no binary was installed")
}
