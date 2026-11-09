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
	return `target=` + c.binaryPath() + `; install_dir="${target%/*}"; staged_host="$install_dir/.attn-pty-host-install"; staged_cli="$install_dir/.attn-cli-install"; `
}

func (s stagedBinaries) installArguments(build bridge.Build) (string, error) {
	raw, err := json.Marshal(build)
	if err != nil {
		return "", err
	}
	buildArg := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`").Replace(string(raw))
	return ` --size ` + fmt.Sprint(s.attnd.size) + ` --sha256 ` + s.attnd.digest + ` --cli "$staged_cli" --cli-size ` + fmt.Sprint(s.attn.size) + ` --cli-sha256 ` + s.attn.digest + ` --host "$staged_host" --host-size ` + fmt.Sprint(s.host.size) + ` --host-sha256 ` + s.host.digest + ` --build "` + buildArg + `" --target "$target"`, nil
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

// Switch makes binaries already staged by Stage the installed ones. The daemon
// restarts on the next bridge dial. markRestart leaves the restart marker that
// tells CLI callers why the daemon is down.
func (c *Client) Switch(ctx context.Context, binaries outpostbuild.Binaries, build bridge.Build, markRestart bool) error {
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
	command := c.locateInstall() + `exec "$install_dir/.attnd-install-` + staged.attnd.digest + `" _outpost-switch` + arguments
	return c.Stream(ctx, command, strings.NewReader(""), func(string) {})
}

func (c *Client) Remove(ctx context.Context, home string) error {
	if err := c.control(ctx, "check"); err != nil {
		return err
	}
	out, err := c.Run(ctx, c.binaryPath()+` enrollment leave --json --home `+home)
	if err != nil {
		return err
	}
	var left enrollment.Result
	if err := json.Unmarshal(out, &left); err != nil {
		return fmt.Errorf("enrollment leave printed %q: %w", out, err)
	}
	if left.Status == "kept" {
		return nil
	}
	return c.StopDaemon(ctx)
}

func (c *Client) StopDaemon(ctx context.Context) error {
	_, err := c.Run(ctx, c.binaryPath()+` daemon stop`)
	return err
}
