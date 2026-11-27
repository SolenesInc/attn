package daemon_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEveryCommandThatTakesAMachineReadsItsNameByOneRule(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "garden_v2_enabled", "true")
	far, _ := connectFarOutpost(t, w, app, answerPlainFolders)
	caller := protocol.SessionID(w.Spawn(app, fakeagent.Claude, w.Path("shop")))

	var opened protocol.TerminalOpenResult
	if err := cli.RunCommand("terminal open", caller, map[string]any{"host": "FAR"}, &opened); err != nil {
		t.Fatalf("terminal open --host FAR: %v", err)
	}
	requireSessionOn(t, cli, opened.SessionID, far)
	t.Logf("terminal open --host FAR started %s on outpost %s", opened.SessionID, far)

	var delegated protocol.DelegationOperation
	if err := cli.RunCommand("delegate", caller, map[string]any{"brief": "Work on far", "cwd": "/far/notes", "model": "default", "request_id": "far-1", "host": "Far"}, &delegated); err != nil {
		t.Fatalf("delegate --host Far: %v", err)
	}
	if got := protocol.Deref(delegated.Error); !strings.Contains(got, "not available on "+farCatalogMachine) {
		t.Fatalf("delegate --host Far: %q, want the outpost's own catalog to decide", got)
	}
	t.Logf("delegate --host Far asked the outpost's catalog: %s", protocol.Deref(delegated.Error))

	var bed protocol.SeedBedResult
	if err := cli.RunCommand("seed bed add", caller, map[string]any{"name": "Far notes", "repos": []string{"Far:/far/notes"}}, &bed); err != nil {
		t.Fatalf("seed bed add --repo Far:/far/notes: %v", err)
	}
	if want := far + ":/far/notes"; !slices.Equal(bed.Bed.Repos, []string{want}) {
		t.Fatalf("seed bed add --repo Far:/far/notes saved %v, want %s", bed.Bed.Repos, want)
	}
	t.Logf("seed bed add --repo Far:/far/notes saved %v", bed.Bed.Repos)

	err := cli.RunCommand("terminal open", caller, map[string]any{"host": "nowhere"}, &opened)
	if err == nil || !strings.Contains(err.Error(), "home") || !strings.Contains(err.Error(), "far") {
		t.Fatalf("terminal open --host nowhere: %v, want a refusal that names the machines", err)
	}
	t.Logf("terminal open --host nowhere: %v", err)

	for name, want := range map[string]string{"FAR": far, "Home": "reserved for the home"} {
		app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: name, Destination: "user@" + strings.ToLower(name) + ".example"})
		refused := testworld.Await(app, protocol.EventCommandError, func(m protocol.CommandErrorMessage) bool {
			return protocol.Deref(m.Cmd) == protocol.CmdAddOutpost
		})
		if !strings.Contains(refused.Error, want) {
			t.Fatalf("add outpost %q: %q, want a refusal naming %s", name, refused.Error, want)
		}
		t.Logf("add outpost %q: %s", name, refused.Error)
	}

	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "twin", Destination: "user@twin.example"})
	twin := testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 2 })
	twinID := twin.Outposts[slices.IndexFunc(twin.Outposts, func(o protocol.OutpostInfo) bool { return o.Name == "twin" })].ID
	homeName := protocol.Deref(twin.HomeName)
	if homeName == "" || protocol.Deref(app.Initial.HomeName) != homeName {
		t.Fatalf("home_name: outposts_updated %q, initial_state %q, want the home's own name in both", homeName, protocol.Deref(app.Initial.HomeName))
	}
	if err := cli.RunCommand("terminal open", caller, map[string]any{"host": strings.ToUpper(homeName)}, &opened); err != nil {
		t.Fatalf("terminal open --host %s: %v", strings.ToUpper(homeName), err)
	}
	requireSessionOn(t, cli, opened.SessionID, "")
	t.Logf("home_name %q, which --host %s reads as the home", homeName, strings.ToUpper(homeName))
	w.stop()
	nameMachineAsAnOlderBuildCould(t, filepath.Join(w.Dir, "attn.db"), twinID, "Far")
	w.start()
	app, cli = w.App(), w.Client()
	caller = protocol.SessionID(w.Spawn(app, fakeagent.Claude, w.Path("shop")))

	err = cli.RunCommand("terminal open", caller, map[string]any{"host": "far"}, &opened)
	if err == nil || !strings.Contains(err.Error(), far) || !strings.Contains(err.Error(), twinID) {
		t.Fatalf("terminal open --host far with two machines named so: %v, want both ids", err)
	}
	t.Logf("terminal open --host far: %v", err)
	err = cli.RunCommand("seed bed add", caller, map[string]any{"name": "Shared", "repos": []string{"far:/far/notes"}}, &bed)
	if err == nil || !strings.Contains(err.Error(), far) || !strings.Contains(err.Error(), twinID) {
		t.Fatalf("seed bed add --repo far:/far/notes with two machines named so: %v, want both ids", err)
	}
	t.Logf("seed bed add --repo far:/far/notes: %v", err)
}

func TestHomeNamesTheHomeBesideAnOlderOutpostNamedHome(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "garden_v2_enabled", "true")
	setSetting(t, app, "outposts_enabled", "true")
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "legacy", Destination: "user@legacy.example"})
	added := testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 1 })
	legacy := added.Outposts[0].ID
	w.stop()
	nameMachineAsAnOlderBuildCould(t, filepath.Join(w.Dir, "attn.db"), legacy, "home")
	w.start()
	app, cli := w.App(), w.Client()
	caller := protocol.SessionID(w.Spawn(app, fakeagent.Claude, w.Path("shop")))

	for _, host := range []string{"home", "HOME"} {
		var opened protocol.TerminalOpenResult
		if err := cli.RunCommand("terminal open", caller, map[string]any{"host": host}, &opened); err != nil {
			t.Fatalf("terminal open --host %s beside outpost %s named home: %v", host, legacy, err)
		}
		requireSessionOn(t, cli, opened.SessionID, "")
		t.Logf("terminal open --host %s beside outpost %s named home started %s on the home", host, legacy, opened.SessionID)
	}

	shop := w.Path("shop")
	var bed protocol.SeedBedResult
	if err := cli.RunCommand("seed bed add", caller, map[string]any{"name": "Shop", "repos": []string{"Home:" + shop}}, &bed); err != nil {
		t.Fatalf("seed bed add --repo Home:%s beside outpost %s named home: %v", shop, legacy, err)
	}
	if want := "home:" + shop; !slices.Equal(bed.Bed.Repos, []string{want}) {
		t.Fatalf("seed bed add --repo Home:%s saved %v, want %s", shop, bed.Bed.Repos, want)
	}
	t.Logf("seed bed add --repo Home:%s saved %v", shop, bed.Bed.Repos)
}

func requireSessionOn(t *testing.T, cli *client.Client, id protocol.SessionID, host string) {
	t.Helper()
	sessions, err := cli.Query("")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(sessions, func(s protocol.Session) bool { return s.ID == id })
	if i < 0 || protocol.Deref(sessions[i].Host) != host {
		t.Fatalf("session %s is not on outpost %s: %+v", id, host, sessions)
	}
}

const farCatalogMachine = "far-host"

// answerPlainFolders answers as an outpost whose folders hold no repository and that offers no agent.
func answerPlainFolders(_ *bridge.Conn, incoming ssh.NewChannel) {
	bridge.Answer(incoming, func(_ context.Context, raw []byte) (any, error) {
		var request protocol.CheckoutInspectMessage
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		switch request.Cmd {
		case protocol.CmdCheckoutInspect:
			return protocol.CheckoutInspection{Cwd: request.Cwd, Exists: true, IsDirectory: true}, nil
		case protocol.CmdAgentCatalog:
			return protocol.AgentCatalogResult{Machine: farCatalogMachine, Harnesses: []protocol.Harness{}}, nil
		}
		return struct{}{}, nil
	})
}

// nameMachineAsAnOlderBuildCould gives a machine a name another one holds, as builds before names were unique let them.
func nameMachineAsAnOlderBuildCould(t *testing.T, path, id, name string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE machines SET name = ? WHERE id = ?`, name, id); err != nil {
		t.Fatal(err)
	}
}
