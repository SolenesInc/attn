package daemon_test

import (
	"database/sql"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/testworld"
)

func TestNoPeerCanReachTheGardenBeforeOlderBodiesAreKept(t *testing.T) {
	home := testworld.NewStack(t)
	home.Start()
	const body = "Only copy, written by an older build"
	planted := home.Attn("seed", "plant", "Older body", "-m", "placeholder")
	if planted.Code != 0 {
		t.Fatalf("plant: %+v", planted)
	}
	id := strings.Fields(strings.TrimSpace(planted.Stdout))[0]
	home.Stop()

	db, err := sql.Open("sqlite3", filepath.Join(home.Dir, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	var collection int64
	if err := db.QueryRow(`SELECT id FROM document_collections WHERE namespace = ? AND collection = ?`, garden.Namespace, garden.CollectionSeeds).Scan(&collection); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE `+docstore.TableName(collection)+` SET body = json_set(body, '$.body', ?), rev = rev + 1 WHERE id = ?`, body, id); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	adopting := home.PauseAt(pausepoint.SeedBodiesAdopting)
	ready := home.StartHeldBeforeReady(adopting)
	if conn, err := net.Dial("unix", filepath.Join(home.Dir, "bridge.sock")); err == nil {
		conn.Close()
		t.Fatal("the bridge accepts peers while older bodies are still being kept")
	}
	adopting.Release()
	ready()
	shown := home.Attn("seed", "show", id)
	if shown.Code != 0 || !strings.Contains(shown.Stdout, body) {
		t.Fatalf("after the start the seed reads %+v, want its body %q", shown, body)
	}
	if parked := home.Attn("seed", "park", id); parked.Code != 0 {
		t.Fatalf("park: %+v", parked)
	}
	if shown := home.Attn("seed", "show", id); !strings.Contains(shown.Stdout, body) {
		t.Fatalf("parking lost the body: %+v", shown)
	}
}
