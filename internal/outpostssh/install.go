package outpostssh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/outpostbuild"
	"github.com/victorarias/attn/internal/ptyhost"
)

type stagedFile struct {
	file   *os.File
	size   int64
	digest string
}

func openStagedFile(path string) (stagedFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return stagedFile{}, err
	}
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err == nil {
		_, err = file.Seek(0, io.SeekStart)
	}
	if err != nil {
		file.Close()
		return stagedFile{}, err
	}
	return stagedFile{file, size, hex.EncodeToString(hash.Sum(nil))}, nil
}

type stagedBinaries struct{ attn, attnd, host stagedFile }

func openStagedBinaries(binaries outpostbuild.Binaries) (stagedBinaries, func(), error) {
	attn, err := openStagedFile(binaries.Attn)
	if err != nil {
		return stagedBinaries{}, nil, err
	}
	attnd, err := openStagedFile(binaries.Attnd)
	if err != nil {
		attn.file.Close()
		return stagedBinaries{}, nil, err
	}
	host, err := openStagedFile(binaries.Host)
	if err != nil {
		attn.file.Close()
		attnd.file.Close()
		return stagedBinaries{}, nil, err
	}
	return stagedBinaries{attn, attnd, host}, func() {
		attn.file.Close()
		attnd.file.Close()
		host.file.Close()
	}, nil
}

func (c *Client) locateInstall() string {
	return c.locate() + `install_dir="${target%/*}"; staged_host="$install_dir/.attn-pty-host-install"; staged_cli="$install_dir/.attn-cli-install"; `
}

func (s stagedBinaries) installArguments(build bridge.Build) (string, error) {
	raw, err := json.Marshal(build)
	if err != nil {
		return "", err
	}
	return ` --size ` + fmt.Sprint(s.attnd.size) + ` --sha256 ` + s.attnd.digest + ` --cli "$staged_cli" --cli-size ` + fmt.Sprint(s.attn.size) + ` --cli-sha256 ` + s.attn.digest + ` --host "$staged_host" --host-size ` + fmt.Sprint(s.host.size) + ` --host-sha256 ` + s.host.digest + ` --build ` + shellWord(string(raw)) + ` --target "$target"`, nil
}

// Stage sends the binaries and has the new attnd check them against build and
// this machine. Nothing installed changes.
func (c *Client) Stage(ctx context.Context, binaries outpostbuild.Binaries, build bridge.Build, phase func(string)) error {
	staged, closeFiles, err := openStagedBinaries(binaries)
	if err != nil {
		return err
	}
	defer closeFiles()
	arguments, err := staged.installArguments(build)
	if err != nil {
		return err
	}
	locate := c.locateInstall()
	phase("sending")
	sendHost := locate + `mkdir -p "$install_dir" && rm -f "$install_dir"/.attnd-install-* && cat > "$staged_host.part" && mv "$staged_host.part" "$staged_host" && chmod 755 "$staged_host"`
	if err := c.Stream(ctx, sendHost, staged.host.file, func(string) {}); err != nil {
		return err
	}
	sendCLI := locate + `cat > "$staged_cli.part" && mv "$staged_cli.part" "$staged_cli" && chmod 755 "$staged_cli"`
	if err := c.Stream(ctx, sendCLI, staged.attn.file, func(string) {}); err != nil {
		return err
	}
	sendAttnd := locate + `staged="$install_dir/.attnd-install-` + staged.attnd.digest + `"; cat > "$staged.part" && mv "$staged.part" "$staged" && chmod 755 "$staged" && exec "$staged" _outpost-install` + arguments
	return c.Stream(ctx, sendAttnd, staged.attnd.file, func(line string) {
		if line == "verifying" {
			phase(line)
		}
	})
}

// Pusher is the machine that pushes its own build to its home. The home names it as the holder of a source
// build's source.
type Pusher struct{ MachineID, Name string }

// Switch makes binaries already staged by Stage the installed ones. The daemon
// restarts on the next bridge dial. markRestart leaves the restart marker that
// tells CLI callers why the daemon is down.
func (c *Client) Switch(ctx context.Context, binaries outpostbuild.Binaries, build bridge.Build, markRestart bool, from *Pusher) error {
	staged, closeFiles, err := openStagedBinaries(binaries)
	if err != nil {
		return err
	}
	defer closeFiles()
	arguments, err := staged.installArguments(build)
	if err != nil {
		return err
	}
	if markRestart {
		arguments += ` --mark-restart`
	}
	if from != nil {
		arguments += ` --from-machine ` + shellWord(from.MachineID) + ` --from-name ` + shellWord(from.Name)
	}
	command := c.locateInstall() + `exec "$install_dir/.attnd-install-` + staged.attnd.digest + `" _outpost-switch` + arguments
	return c.Stream(ctx, command, strings.NewReader(""), func(string) {})
}

// PutCache sends binaries into the outpost build cache of the home at the destination, as the binaries of build
// for platform.
func (c *Client) PutCache(ctx context.Context, binaries outpostbuild.Binaries, build bridge.Build, platform string) error {
	staged, closeFiles, err := openStagedBinaries(binaries)
	if err != nil {
		return err
	}
	defer closeFiles()
	raw, err := json.Marshal(build)
	if err != nil {
		return err
	}
	for _, file := range []struct {
		name   string
		staged stagedFile
	}{{"attn", staged.attn}, {"attnd", staged.attnd}, {ptyhost.BinaryName, staged.host}} {
		command := c.locate() + `exec "${target%/*}/attnd" _outpost-cache-put --build ` + shellWord(string(raw)) + ` --platform ` + shellWord(platform) + ` --name ` + file.name + ` --size ` + fmt.Sprint(file.staged.size) + ` --sha256 ` + file.staged.digest
		if err := c.Stream(ctx, command, file.staged.file, func(string) {}); err != nil {
			return fmt.Errorf("send %s to the home's build cache: %w", file.name, err)
		}
	}
	return nil
}

// shellWord quotes value for the double-quoted context of a remote script, which holds no single quotes.
func shellWord(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`").Replace(value) + `"`
}

func (c *Client) Remove(ctx context.Context, home enrollment.Home) error {
	if err := c.control(ctx, "check"); err != nil {
		return err
	}
	out, err := c.Run(ctx, c.binaryPath()+` enrollment status --json`)
	if err != nil {
		return err
	}
	var status enrollment.Status
	if err := json.Unmarshal(out, &status); err != nil {
		return fmt.Errorf("enrollment status printed %q: %w", out, err)
	}
	if status.CheckHome(home) != nil {
		return nil
	}
	if _, err := c.Run(ctx, c.binaryPath()+` enrollment leave --json`); err != nil {
		return err
	}
	return c.StopDaemon(ctx)
}

func (c *Client) StopDaemon(ctx context.Context) error {
	_, err := c.Run(ctx, c.binaryPath()+` daemon stop`)
	return err
}
