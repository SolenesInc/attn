package daemon_test

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestGardenV2OwnedBindingAddressesInTheGardenForm(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	setSetting(t, app, "garden_v2_enabled", "true")
	billing := newRepo(t, "billing")
	shop := filepath.Join(t.TempDir(), "shop")
	if err := os.Symlink(billing, shop); err != nil {
		t.Fatal(err)
	}
	host, peer := connectGardenOwnedPathPeer(t, w, app)

	local := saveGardenOwnedPathBed(t, app, nil, "Home default", []string{shop}, "plain path")
	if got := local.Repos; len(got) != 1 || got[0] != "home:"+billing {
		t.Fatalf("unqualified form saved %v, want home:%s", got, billing)
	}
	t.Log("Unqualified path in the home form resolved the home symlink")
	assertOwnedPathRoute(t, w, nil, billing, "Home default")
	assertOwnedPathRoute(t, w, peer, shop, "")

	remote := saveGardenOwnedPathBed(t, app, nil, "Shop", []string{"far:" + shop}, "named outpost")
	if got := remote.Repos; len(got) != 1 || got[0] != host+":"+shop {
		t.Fatalf("qualified form saved %v, want stable outpost ID and exact Shop path", got)
	}
	saveGardenOwnedPathBed(t, app, &remote.ID, remote.Name, remote.Repos, "note only edit")
	assertOwnedPathRoute(t, w, nil, billing, "Home default")
	assertOwnedPathRoute(t, w, peer, shop, "Shop")

	collision := newRepo(t, "collision")
	saveGardenOwnedPathBed(t, app, nil, "Home collision", []string{"home:" + collision}, "home scope")
	saveGardenOwnedPathBed(t, app, nil, "Outpost collision", []string{"far:" + collision}, "outpost scope")
	assertOwnedPathRoute(t, w, nil, collision, "Home collision")
	assertOwnedPathRoute(t, w, peer, collision, "Outpost collision")
	var refused protocol.SeedBedResult
	if err := runGardenOwnedPathCommand(t, w, peer, collision, "seed bed add", map[string]any{
		"name": "A duplicate within the outpost", "repos": []string{collision},
	}, &refused); err == nil || !strings.Contains(err.Error(), "Outpost collision") {
		t.Fatalf("duplicate on the same machine: %v", err)
	}
	t.Log("Duplicate refused within one machine")

	shared := newRepo(t, "shared")
	saveGardenOwnedPathBed(t, app, nil, "Shared", []string{"home:" + shared, "far:" + shared}, "both owned paths")
	assertOwnedPathRoute(t, w, nil, shared, "Shared")
	assertOwnedPathRoute(t, w, peer, shared, "Shared")

	portable := newRepo(t, "portable")
	saveGardenOwnedPathBed(t, app, nil, "Bare name", []string{"portable"}, "all checkout names")
	assertOwnedPathRoute(t, w, nil, portable, "Bare name")
	assertOwnedPathRoute(t, w, peer, filepath.Join(t.TempDir(), "portable"), "Bare name")

	w.restart()
	app = w.App()
	saved := saveGardenOwnedPathBed(t, app, &remote.ID, remote.Name, remote.Repos, "note after restart")
	if got := saved.Repos; len(got) != 1 || got[0] != host+":"+shop {
		t.Fatalf("outpost binding after restart = %v, want %s:%s", got, host, shop)
	}
	assertOwnedPathRoute(t, w, nil, collision, "Home collision")
}

func saveGardenOwnedPathBed(t *testing.T, app *testworld.Peer, id *string, name string, repos []string, note string) protocol.GardenBed {
	t.Helper()
	request := "save-" + name + "-" + note
	saved := testworld.Request(app, protocol.GardenBedSaveMessage{
		Cmd: protocol.CmdGardenBedSave, RequestID: request, ID: id, Name: name, Repos: repos, Note: &note,
	}, protocol.EventGardenBedSaveResult, func(reply protocol.GardenBedSaveResultMessage) bool {
		return reply.RequestID == request
	})
	if !saved.Success || saved.Bed == nil {
		t.Fatalf("save %s: %s", name, protocol.Deref(saved.Error))
	}
	return *saved.Bed
}

func connectGardenOwnedPathPeer(t *testing.T, w *world, app *testworld.Peer) (string, *bridge.Conn) {
	t.Helper()
	return connectFarOutpost(t, w, app, func(_ *bridge.Conn, incoming ssh.NewChannel) {
		bridge.Answer(incoming, func(_ context.Context, raw []byte) (any, error) {
			var request protocol.CheckoutInspectMessage
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			if request.Cmd == protocol.CmdCheckoutInspect {
				return protocol.CheckoutInspection{
					Cwd: request.Cwd, Exists: true, IsDirectory: true, MainRepo: request.Cwd,
				}, nil
			}
			return struct{}{}, nil
		})
	})
}

func assertOwnedPathRoute(t *testing.T, w *world, peer *bridge.Conn, dir, want string) {
	t.Helper()
	var planted protocol.SeedPlantResult
	if err := runGardenOwnedPathCommand(t, w, peer, dir, "seed plant", map[string]string{"title": "Path routing probe"}, &planted); err != nil {
		t.Fatal(err)
	}
	shown, err := w.Client().SeedShow("", planted.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := protocol.Deref(shown.Seed.Bed)
	if got != want {
		t.Errorf("plant from %s belongs to %q, want %q", dir, got, want)
	}
	place := "home"
	if peer != nil {
		place = "outpost"
	}
	t.Logf("%s %s -> %s", place, dir, cmp.Or(got, "Unassigned"))
}

func runGardenOwnedPathCommand(t *testing.T, w *world, peer *bridge.Conn, dir, command string, args any, result any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), fakeagent.HangGuard)
	defer cancel()
	var conn io.ReadWriteCloser
	if peer == nil {
		local, err := w.DialUnix()
		if err != nil {
			return err
		}
		conn = local
	} else {
		channel, requests, err := peer.Open(ctx, "forward", []byte(frozenForwardHeader))
		if err != nil {
			return err
		}
		go ssh.DiscardRequests(requests)
		conn = channel
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	request := protocol.RunCommandMessage{Cmd: protocol.CmdRunCommand, Command: command, Directory: &dir, Args: raw}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return err
	}
	response := struct {
		Ok      bool                     `json:"ok"`
		Result  any                      `json:"command_result"`
		Refusal *protocol.CommandRefusal `json:"command_refusal"`
		Error   string                   `json:"error"`
	}{Result: result}
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return err
	}
	if response.Ok {
		return nil
	}
	return fmt.Errorf("%s: %+v %s", command, response.Refusal, response.Error)
}
