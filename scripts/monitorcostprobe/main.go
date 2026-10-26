package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
	"slices"

	_ "github.com/mattn/go-sqlite3"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

func main() {
	path := flag.String("db", "", "read-only path to a copy of attn.db")
	fixture := flag.String("fixture", "", "write legacy cost views for reproducible before/after benchmarks")
	allSessions := flag.Bool("all-sessions", false, "compare totals for every session, including closed sessions and shells")
	flag.Parse()
	if *path == "" {
		flag.Usage()
		os.Exit(2)
	}
	db, err := sql.Open("sqlite3", "file:"+*path+"?mode=ro")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	query := "SELECT id, session_cost_json FROM sessions_with_runtime"
	if !*allSessions {
		query += " WHERE closed_at = '' AND agent != 'shell'"
	}
	rows, err := db.Query(query + " ORDER BY id")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	var sizes []int
	var fixtures []json.RawMessage
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			log.Fatal(err)
		}
		var checkpoint sessioncost.Checkpoint
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &checkpoint); err != nil {
				log.Fatal(err)
			}
		}
		input, err := json.Marshal(map[string]any{"CostInitialized": map[string]any{"V": checkpoint.Initialized}, "Cost": map[string]any{"Entries": checkpoint.Sources}})
		if err != nil {
			log.Fatal(err)
		}
		fixtures = append(fixtures, input)
		var view sessionmonitor.View
		if err := json.Unmarshal(input, &view); err != nil {
			log.Fatal(err)
		}
		encoded, err := json.Marshal(view)
		if err != nil {
			log.Fatal(err)
		}
		var recovered struct {
			Cost struct {
				Entries map[string]*sessioncost.Source
			}
			CostInitialized struct{ V bool }
		}
		if err := json.Unmarshal(encoded, &recovered); err != nil {
			log.Fatal(err)
		}
		got := (sessioncost.Checkpoint{Initialized: recovered.CostInitialized.V, Sources: recovered.Cost.Entries}).Usage()
		if !reflect.DeepEqual(got, checkpoint.Usage()) {
			log.Fatalf("cost totals differ for session %s", id)
		}
		sizes = append(sizes, len(encoded))
		fmt.Printf("session=%s view_bytes=%d totals=identical\n", id, len(encoded))
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}
	if *fixture != "" {
		encoded, err := json.Marshal(fixtures)
		if err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(*fixture, encoded, 0600); err != nil {
			log.Fatal(err)
		}
	}
	slices.Sort(sizes)
	if len(sizes) == 0 {
		log.Fatal("copy has no open agent sessions")
	}
	fmt.Printf("views=%d p50_bytes=%d p90_bytes=%d max_bytes=%d all_cost_totals=identical\n", len(sizes), sizes[(len(sizes)-1)/2], sizes[(len(sizes)-1)*9/10], sizes[len(sizes)-1])
}
