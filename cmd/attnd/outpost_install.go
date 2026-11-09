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
	"github.com/victorarias/attn/internal/selfupdate"
	"github.com/victorarias/attn/internal/store"
)

func runOutpostInstall() {
	if err := stageOutpostBinaries(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}
}

func runOutpostSwitch() {
	if err := switchOutpostBinaries(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}
}

type outpostInstall struct {
	staged, cli, host, target string
	markRestart               bool
}

func (i outpostInstall) removeStaged() {
	os.Remove(i.staged)
	os.Remove(i.cli)
	os.Remove(i.host)
}

func stageOutpostBinaries(args []string) error {
	install, err := checkedOutpostInstall("_outpost-install", args)
	if err != nil {
		install.removeStaged()
		return err
	}
	fmt.Println("staged")
	return nil
}

func switchOutpostBinaries(args []string) error {
	install, err := checkedOutpostInstall("_outpost-switch", args)
	defer install.removeStaged()
	if err != nil {
		return err
	}
	if install.markRestart {
		if err := selfupdate.WriteRestartMarker(config.DataDir(), "attn is switching to the home's new build"); err != nil {
			return err
		}
	}
	dir := filepath.Dir(install.target)
	if err := replaceKeepingPrevious(install.host, filepath.Join(dir, ptyhost.BinaryName)); err != nil {
		return err
	}
	if err := replaceKeepingPrevious(install.staged, filepath.Join(dir, "attnd")); err != nil {
		return err
	}
	if err := replaceKeepingPrevious(install.cli, install.target); err != nil {
		return err
	}
	fmt.Println("installed")
	return nil
}

// checkedOutpostInstall returns the paths it parsed even when a check fails,
// so the caller can remove what was sent.
func checkedOutpostInstall(name string, args []string) (outpostInstall, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
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
	markRestart := flags.Bool("mark-restart", false, "Leave the restart marker")
	var install outpostInstall
	if err := flags.Parse(args); err != nil {
		return install, err
	}
	var expected bridge.Build
	if err := json.Unmarshal([]byte(*build), &expected); err != nil {
		return install, fmt.Errorf("expected build: %w", err)
	}
	if *size <= 0 || *digest == "" || *cli == "" || *cliSize <= 0 || *cliDigest == "" || *host == "" || *hostSize <= 0 || *hostDigest == "" || *target == "" || flags.NArg() != 0 {
		return install, fmt.Errorf("%s requires --size, --sha256, --cli, --cli-size, --cli-sha256, --host, --host-size, --host-sha256, --build and --target", name)
	}
	staged, err := os.Executable()
	if err != nil {
		return install, err
	}
	installedDaemon := filepath.Join(filepath.Dir(*target), "attnd")
	if filepath.Clean(staged) == filepath.Clean(installedDaemon) {
		return install, fmt.Errorf("run %s from a staged binary, not the install path %s", name, installedDaemon)
	}
	install = outpostInstall{staged: staged, cli: *cli, host: *host, target: *target, markRestart: *markRestart}
	fmt.Println("verifying")
	if err := verifyStagedBinary(staged, *size, *digest); err != nil {
		return install, err
	}
	if err := verifyStagedBinary(*cli, *cliSize, *cliDigest); err != nil {
		return install, fmt.Errorf("attn: %w", err)
	}
	if err := verifyStagedBinary(*host, *hostSize, *hostDigest); err != nil {
		return install, fmt.Errorf("%s: %w", ptyhost.BinaryName, err)
	}
	actual := bridge.Build{Version: buildinfo.Version, SourceFingerprint: buildinfo.SourceFingerprint, ProtocolVersion: protocol.ProtocolVersion}
	if actual != expected {
		return install, fmt.Errorf("staged build %s differs from home's build %s", actual, expected)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return install, err
	}
	if os.Getenv("ATTN_HARNESS_DATA_DIR") != "" && os.Getenv("ATTN_HARNESS_OUTPOST_INSTALL_ROOT") != "" {
		home = os.Getenv("ATTN_HARNESS_OUTPOST_INSTALL_ROOT")
	}
	if err := outpostbuild.CheckInstallTarget(config.InstanceLabel(), *target, home); err != nil {
		return install, err
	}
	if err := store.CheckSchema(config.DBPath()); err != nil {
		return install, err
	}
	return install, nil
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
