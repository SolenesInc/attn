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
)

func (c *Client) Install(ctx context.Context, path string, build bridge.Build, phase func(string)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	raw, err := json.Marshal(build)
	if err != nil {
		return err
	}
	buildArg := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`").Replace(string(raw))
	script := `target=` + c.binaryPath() + `; install_dir="${target%/*}"; staged="$install_dir/.attn-install-` + digest + `"; mkdir -p "$install_dir" && cat > "$staged.part" && mv "$staged.part" "$staged" && chmod 755 "$staged" && exec "$staged" _outpost-install --size ` + fmt.Sprint(size) + ` --sha256 ` + digest + ` --build "` + buildArg + `" --target "$target"`
	phase("sending")
	return c.Stream(ctx, script, file, func(line string) {
		if line == "verifying" {
			phase(line)
		}
	})
}

func (c *Client) Remove(ctx context.Context) error {
	_, err := c.Run(ctx, `if test -x `+c.binaryPath()+`; then `+c.binaryPath()+` enrollment leave && `+c.binaryPath()+` daemon stop; fi`)
	return err
}
