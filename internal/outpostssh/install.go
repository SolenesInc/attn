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

func (c *Client) Install(ctx context.Context, binaries outpostbuild.Binaries, build bridge.Build, phase func(string)) error {
	attn, err := openStagedFile(binaries.Attn)
	if err != nil {
		return err
	}
	defer attn.file.Close()
	host, err := openStagedFile(binaries.Host)
	if err != nil {
		return err
	}
	defer host.file.Close()
	raw, err := json.Marshal(build)
	if err != nil {
		return err
	}
	buildArg := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`").Replace(string(raw))
	locate := `target=` + c.binaryPath() + `; install_dir="${target%/*}"; staged_host="$install_dir/.attn-pty-host-install"; `
	phase("sending")
	sendHost := locate + `mkdir -p "$install_dir" && cat > "$staged_host.part" && mv "$staged_host.part" "$staged_host" && chmod 755 "$staged_host"`
	if err := c.Stream(ctx, sendHost, host.file, func(string) {}); err != nil {
		return err
	}
	sendAttn := locate + `staged="$install_dir/.attn-install-` + attn.digest + `"; cat > "$staged.part" && mv "$staged.part" "$staged" && chmod 755 "$staged" && exec "$staged" _outpost-install --size ` + fmt.Sprint(attn.size) + ` --sha256 ` + attn.digest + ` --host "$staged_host" --host-size ` + fmt.Sprint(host.size) + ` --host-sha256 ` + host.digest + ` --build "` + buildArg + `" --target "$target"`
	return c.Stream(ctx, sendAttn, attn.file, func(line string) {
		if line == "verifying" {
			phase(line)
		}
	})
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
