package main

import (
	"fmt"
	"io"
	"os"

	"github.com/victorarias/attn/internal/store"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: migration-fixture <database>, with fixture SQL on stdin")
		os.Exit(2)
	}
	if err := apply(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "migration-fixture:", err)
		os.Exit(1)
	}
}

func apply(path string) error {
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	db, err := store.OpenDB(path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(string(body))
	return err
}
