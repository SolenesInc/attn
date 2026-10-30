package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/outpostbuild"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func runOutpostInstall() {
	if err := installOutpostBinary(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func installOutpostBinary(args []string) error {
	flags := flag.NewFlagSet("_outpost-install", flag.ContinueOnError)
	size := flags.Int64("size", 0, "Expected binary size")
	digest := flags.String("sha256", "", "Expected binary sha256")
	build := flags.String("build", "", "Expected build JSON")
	target := flags.String("target", "", "Install path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	var expected bridge.Build
	if err := json.Unmarshal([]byte(*build), &expected); err != nil {
		return fmt.Errorf("expected build: %w", err)
	}
	if *size <= 0 || *digest == "" || *target == "" || flags.NArg() != 0 {
		return fmt.Errorf("_outpost-install requires --size, --sha256, --build and --target")
	}
	staged, err := os.Executable()
	if err != nil {
		return err
	}
	if filepath.Clean(staged) == filepath.Clean(*target) {
		return fmt.Errorf("run _outpost-install from a staged binary, not the install path %s", *target)
	}
	defer os.Remove(staged)
	fmt.Println("verifying")
	file, err := os.Open(staged)
	if err != nil {
		return err
	}
	hash := sha256.New()
	actualSize, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if actualSize != *size {
		return fmt.Errorf("install size=%d, received %d bytes", *size, actualSize)
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); actual != *digest {
		return fmt.Errorf("install sha256=%s, received %s", *digest, actual)
	}
	actual := bridge.Build{Version: buildinfo.Version, SourceFingerprint: buildinfo.SourceFingerprint, ProtocolVersion: protocol.ProtocolVersion}
	if actual != expected {
		return fmt.Errorf("staged build %s differs from home's build %s", actual, expected)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if os.Getenv("ATTN_HARNESS_DATA_DIR") != "" && os.Getenv("ATTN_HARNESS_OUTPOST_INSTALL_ROOT") != "" {
		home = os.Getenv("ATTN_HARNESS_OUTPOST_INSTALL_ROOT")
	}
	if err := outpostbuild.CheckInstallTarget(config.InstanceLabel(), *target, home); err != nil {
		return err
	}
	if err := store.CheckSchema(config.DBPath()); err != nil {
		return err
	}
	if _, err := os.Stat(config.DBPath()); err == nil {
		u := &url.URL{Scheme: "file", Path: config.DBPath(), RawQuery: "mode=ro"}
		db, err := sql.Open("sqlite3", u.String())
		if err != nil {
			return err
		}
		var value string
		err = db.QueryRow(`SELECT value FROM settings WHERE key = ?`, "pty_shared_host_enabled").Scan(&value)
		db.Close()
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if value == "true" {
			return fmt.Errorf("outpost has pty_shared_host_enabled on; this install carries attn only and would leave attn-pty-host on the old build")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	previous := *target + ".previous"
	if err := os.Remove(previous); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Link(*target, previous); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("keep previous binary: %w", err)
	}
	if err := os.Rename(staged, *target); err != nil {
		return fmt.Errorf("replace outpost binary: %w", err)
	}
	fmt.Println("installed")
	return nil
}
