package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/outpostbuild"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/store"
)

func runOutpostInstall() {
	if err := installOutpostBinary(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}
}

func installOutpostBinary(args []string) error {
	flags := flag.NewFlagSet("_outpost-install", flag.ContinueOnError)
	size := flags.Int64("size", 0, "Expected binary size")
	digest := flags.String("sha256", "", "Expected binary sha256")
	cli := flags.String("cli", "", "Staged attn CLI path")
	cliSize := flags.Int64("cli-size", 0, "Expected attn CLI size")
	cliDigest := flags.String("cli-sha256", "", "Expected attn CLI sha256")
	host := flags.String("host", "", "Staged attn-pty-host path")
	hostSize := flags.Int64("host-size", 0, "Expected attn-pty-host size")
	hostDigest := flags.String("host-sha256", "", "Expected attn-pty-host sha256")
	build := flags.String("build", "", "Expected build JSON")
	target := flags.String("target", "", "Install path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	var expected bridge.Build
	if err := json.Unmarshal([]byte(*build), &expected); err != nil {
		return fmt.Errorf("expected build: %w", err)
	}
	if *size <= 0 || *digest == "" || *cli == "" || *cliSize <= 0 || *cliDigest == "" || *host == "" || *hostSize <= 0 || *hostDigest == "" || *target == "" || flags.NArg() != 0 {
		return fmt.Errorf("_outpost-install requires --size, --sha256, --cli, --cli-size, --cli-sha256, --host, --host-size, --host-sha256, --build and --target")
	}
	staged, err := os.Executable()
	if err != nil {
		return err
	}
	installedDaemon := filepath.Join(filepath.Dir(*target), "attnd")
	if filepath.Clean(staged) == filepath.Clean(installedDaemon) {
		return fmt.Errorf("run _outpost-install from a staged binary, not the install path %s", installedDaemon)
	}
	defer os.Remove(staged)
	defer os.Remove(*cli)
	defer os.Remove(*host)
	fmt.Println("verifying")
	if err := verifyStagedBinary(staged, *size, *digest); err != nil {
		return err
	}
	if err := verifyStagedBinary(*cli, *cliSize, *cliDigest); err != nil {
		return fmt.Errorf("attn: %w", err)
	}
	if err := verifyStagedBinary(*host, *hostSize, *hostDigest); err != nil {
		return fmt.Errorf("%s: %w", ptyhost.BinaryName, err)
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
	if err := replaceKeepingPrevious(*host, filepath.Join(filepath.Dir(*target), ptyhost.BinaryName)); err != nil {
		return err
	}
	if err := replaceKeepingPrevious(staged, installedDaemon); err != nil {
		return err
	}
	if err := replaceKeepingPrevious(*cli, *target); err != nil {
		return err
	}
	fmt.Println("installed")
	return nil
}

func verifyStagedBinary(path string, size int64, digest string) error {
	file, err := os.Open(path)
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
	if actualSize != size {
		return fmt.Errorf("install size=%d, received %d bytes", size, actualSize)
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); actual != digest {
		return fmt.Errorf("install sha256=%s, received %s", digest, actual)
	}
	return nil
}

func replaceKeepingPrevious(staged, target string) error {
	if _, err := os.Stat(target); err == nil {
		previous := target + ".previous"
		if err := os.Remove(previous); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Link(target, previous); err != nil {
			return fmt.Errorf("keep previous binary: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}
	return nil
}
