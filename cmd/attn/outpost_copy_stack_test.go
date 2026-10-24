package main_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type copyTables map[string]map[string]any

func readRows(t *testing.T, s *testworld.Stack, query string, args ...any) copyTables {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(s.Dir, "attn.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	out := copyTables{}
	for rows.Next() {
		values := make([]any, len(names))
		targets := make([]any, len(names))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		row := map[string]any{}
		for i, name := range names[2:] {
			if name != "copied_from" {
				row[name] = values[i+2]
			}
		}
		out[fmt.Sprintf("%s %s", values[0], values[1])] = row
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func hostRows(t *testing.T, s *testworld.Stack, host string) copyTables {
	t.Helper()
	all := readRows(t, s, `SELECT 'sessions', id, * FROM sessions WHERE host = ?`, host)
	for key, row := range readRows(t, s, `SELECT 'terminal_bindings', b.terminal_id, b.* FROM terminal_bindings b JOIN sessions s ON s.id = b.session_id WHERE s.host = ?`, host) {
		all[key] = row
	}
	return all
}

func copiedRows(t *testing.T, s *testworld.Stack) copyTables {
	t.Helper()
	all := readRows(t, s, `SELECT 'sessions', id, * FROM sessions WHERE host != ''`)
	for key, row := range readRows(t, s, `SELECT 'terminal_bindings', terminal_id, * FROM terminal_bindings`) {
		all[key] = row
	}
	return all
}

func outbox(t *testing.T, s *testworld.Stack) copyTables {
	t.Helper()
	return readRows(t, s, `SELECT tbl, key, host FROM outpost_outbox`)
}

func TestOutpostCopyFollowsTheHomeForItsTerminalsOnly(t *testing.T) {
	a := testworld.NewSSHAgent(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	for _, s := range []*testworld.Stack{remote, home} {
		a.Authorize(s.World)
	}
	staged := remote.PauseEvery(pausepoint.CopyBatchStaged)
	applied := remote.PauseEvery(pausepoint.CopyBatchApplied)
	remote.Start()
	home.Start()
	address := bridgeAddress(t)
	requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
	p := enableOutposts(t, home)
	p.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: address})
	outpost := outpostState(p, "outpost", protocol.OutpostStateConnected)
	host := outpost.ID

	awaitCopy := func(atMatch func()) {
		t.Helper()
		for {
			staged.Await()
			staged.Release()
			applied.Await()
			want, got := hostRows(t, home, host), copiedRows(t, remote)
			matched := reflect.DeepEqual(want, got)
			if matched && atMatch != nil {
				atMatch()
			}
			applied.Release()
			if matched {
				return
			}
		}
	}
	priority := func(on bool) {
		t.Helper()
		p.Send(protocol.SetSessionPriorityMessage{Cmd: protocol.CmdSetSessionPriority, SessionID: "remote-shell", Priority: on})
		testworld.AwaitSession(p, "remote-shell", func(s protocol.Session) bool { return protocol.Deref(s.Priority) == on })
	}

	if err := home.InjectSessionOn(outpost.ID, "remote-shell", "remote shell", home.Path("remote"), protocol.SessionAgentShell); err != nil {
		t.Fatal(err)
	}
	if err := home.InjectSession("local-shell", "local shell", home.Path("local"), protocol.SessionAgentShell); err != nil {
		t.Fatal(err)
	}
	awaitCopy(nil)
	created := copiedRows(t, remote)
	var bindings int
	for key, row := range created {
		if row["session_id"] == "remote-shell" {
			bindings++
		}
		if key == "sessions local-shell" || row["session_id"] == "local-shell" {
			t.Fatalf("a row without a host reached the outpost: %s", key)
		}
	}
	if created["sessions remote-shell"] == nil || bindings != 1 {
		t.Fatalf("copy after create: %v", created)
	}
	origin := readRows(t, remote, `SELECT 'sessions', id, copied_from AS origin FROM sessions WHERE id = 'remote-shell'`)["sessions remote-shell"]["origin"]
	if origin != protocol.Deref(p.Initial.DaemonInstanceID) {
		t.Fatalf("copy marked as copied from %v, want the home %s", origin, protocol.Deref(p.Initial.DaemonInstanceID))
	}

	priority(true)
	awaitCopy(nil)
	priority(false)
	awaitCopy(func() {
		want := copyTables{"sessions remote-shell": {"host": outpost.ID}}
		if got := outbox(t, home); !reflect.DeepEqual(got, want) {
			t.Fatalf("outbox while the last batch is in flight = %v, want only that batch: %v", got, want)
		}
	})
	before := copiedRows(t, remote)

	priority(true)
	staged.Await()
	if got := copiedRows(t, remote); !reflect.DeepEqual(got, before) {
		t.Fatalf("a staged batch showed before commit: %v", got)
	}
	remote.Kill()
	staged.Release()
	home.Stop()
	if got := copiedRows(t, remote); !reflect.DeepEqual(got, before) {
		t.Fatalf("killing the outpost mid-batch left a partial copy: %v", got)
	}
	remote.Start()
	remote.App()
	if got := copiedRows(t, remote); !reflect.DeepEqual(got, before) {
		t.Fatalf("outpost restart changed copied rows:\n got %v\nwant %v", got, before)
	}
	home.Start()
	p = home.App()
	outpostState(p, "outpost", protocol.OutpostStateConnected)
	awaitCopy(nil)
	if got := copiedRows(t, remote)["sessions remote-shell"]["priority"]; got != int64(1) {
		t.Fatalf("resent batch not applied: priority %v", got)
	}

	testworld.Request(p, protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: "remote-shell"}, protocol.EventSessionCloseResult,
		func(r protocol.SessionCloseResultMessage) bool { return r.SessionID == "remote-shell" })
	awaitCopy(nil)

	remote.Stop()
	if err := home.InjectSessionOn(outpost.ID, "pending-shell", "pending shell", home.Path("pending"), protocol.SessionAgentShell); err != nil {
		t.Fatal(err)
	}
	if pending := outbox(t, home); len(pending) == 0 {
		t.Fatal("no outbox entries pending for an unreachable outpost")
	}
	p.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: outpost.ID})
	testworld.Await[protocol.OutpostsUpdatedMessage](p, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	if pending := outbox(t, home); len(pending) != 0 {
		t.Fatalf("Remove left outbox entries: %v", pending)
	}

	if len(copiedRows(t, remote)) == 0 {
		t.Fatal("no copy left on the outpost to drop")
	}
	requireStdout(t, remote.Attn("enrollment", "leave"), "no longer enrolled")
	remote.Start()
	remote.App()
	if got := copiedRows(t, remote); len(got) != 0 {
		t.Fatalf("a daemon started after an offline leave kept the copy: %v", got)
	}

	p.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "again", Address: address})
	host = outpostState(p, "again", protocol.OutpostStateConnected).ID
	if err := home.InjectSessionOn(host, "second-shell", "second shell", home.Path("second"), protocol.SessionAgentShell); err != nil {
		t.Fatal(err)
	}
	awaitCopy(nil)
	requireStdout(t, remote.Attn("enrollment", "leave"), "no longer enrolled")
	if got := copiedRows(t, remote); len(got) != 0 {
		t.Fatalf("attn enrollment leave on a running outpost kept the copy: %v", got)
	}
	requireStdout(t, home.Attn("enrollment", "leave"), "not enrolled")
	if kept := hostRows(t, home, host); kept["sessions second-shell"] == nil {
		t.Fatalf("attn enrollment leave on the home dropped its own records: %v", kept)
	}
}
