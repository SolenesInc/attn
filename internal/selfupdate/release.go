package selfupdate

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/victorarias/attn/internal/outpostbuild"
	"github.com/victorarias/attn/internal/ptyhost"
)

const (
	BundleName = "attn.app"
	appArchive = "attn_aarch64.app.tar.gz"
)

func newReleasePlan(install Install, dataDir string) (Plan, error) {
	plan := Plan{Kind: install.Kind, Dir: filepath.Join(dataDir, "update-release")}
	executable := install.Facts.Executable
	if install.Kind == ReleaseApp {
		bundleEnd := strings.Index(executable, ".app/Contents/MacOS/") + len(".app")
		plan.Root = executable[:bundleEnd]
		return plan, nil
	}
	if runtime.GOOS != "linux" {
		return Plan{}, fmt.Errorf("attn publishes no %s release binaries, so %s cannot update itself. Install the app from the releases page", runtime.GOOS, executable)
	}
	plan.Root = filepath.Dir(executable)
	return plan, nil
}

// Download puts the latest release in Dir when it is newer than current and
// passes its integrity check. On a refusal Dir is left empty and the install
// is untouched. It reports false with nothing downloaded when current is the
// latest.
func (p Plan) Download(ctx context.Context, releaseSource, current string, env []string, log io.Writer) (Release, bool, error) {
	latest, err := FindLatest(ctx, releaseSource)
	if err != nil {
		return Release{}, false, fmt.Errorf("could not find the latest release: %w", err)
	}
	if !IsNewer(current, latest.Version) {
		return latest, false, nil
	}
	if err := os.RemoveAll(p.Dir); err != nil {
		return Release{}, false, fmt.Errorf("clear %s: %w", p.Dir, err)
	}
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return Release{}, false, fmt.Errorf("create %s: %w", p.Dir, err)
	}
	fmt.Fprintf(log, ">>> downloading release %s from %s\n", latest.Version, releaseSource)
	if p.Kind == ReleaseApp {
		err = p.downloadApp(ctx, releaseSource, latest.Version, env, log)
	} else {
		err = p.downloadBinaries(ctx, releaseSource, latest.Version)
	}
	if err != nil {
		_ = os.RemoveAll(p.Dir)
		return Release{}, false, err
	}
	return latest, true, nil
}

func (p Plan) downloadBinaries(ctx context.Context, releaseSource, version string) error {
	arch := runtime.GOARCH
	staged := outpostbuild.BinariesIn(p.Dir)
	if err := outpostbuild.DownloadRelease(ctx, releaseSource, version, arch, staged); err != nil {
		return err
	}
	sumsName := "SHA256SUMS-linux-" + arch
	sumsPath := filepath.Join(p.Dir, sumsName)
	if err := outpostbuild.Download(ctx, outpostbuild.ReleaseURL(releaseSource, version)+sumsName, sumsPath); err != nil {
		return err
	}
	sums, err := readSums(sumsPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", sumsName, err)
	}
	for asset, path := range map[string]string{
		"attnd-linux-" + arch:                 staged.Attnd,
		"attn-linux-" + arch:                  staged.Attn,
		ptyhost.BinaryName + "-linux-" + arch: staged.Host,
	} {
		if err := checkSum(path, asset, sums[asset], version); err != nil {
			return err
		}
	}
	return os.Remove(sumsPath)
}

func readSums(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	sums := map[string]string{}
	lines := bufio.NewScanner(file)
	for lines.Scan() {
		if fields := strings.Fields(lines.Text()); len(fields) == 2 {
			sums[strings.TrimPrefix(fields[1], "*")] = fields[0]
		}
	}
	return sums, lines.Err()
}

func checkSum(path, asset, want, version string) error {
	if want == "" {
		return fmt.Errorf("release %s lists no checksum for %s, so it was not installed", version, asset)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("read the downloaded %s: %w", asset, err)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		return fmt.Errorf("the downloaded %s does not match release %s (sha256 %s, want %s), so it was not installed", asset, version, got, want)
	}
	return nil
}

func (p Plan) downloadApp(ctx context.Context, releaseSource, version string, env []string, log io.Writer) error {
	archive := filepath.Join(p.Dir, appArchive)
	if err := outpostbuild.Download(ctx, outpostbuild.ReleaseURL(releaseSource, version)+appArchive, archive); err != nil {
		return err
	}
	if err := runIn(ctx, p.Dir, env, log, "tar", "-xzf", archive); err != nil {
		return fmt.Errorf("the downloaded %s could not be unpacked: %w", appArchive, err)
	}
	if err := os.Remove(archive); err != nil {
		return err
	}
	requirement, err := p.installedRequirement(ctx, env)
	if err != nil {
		return err
	}
	staged := filepath.Join(p.Dir, BundleName)
	if err := runIn(ctx, p.Dir, env, log, "codesign", "--verify", "--deep", "--strict", "-R", "="+requirement, staged); err != nil {
		return fmt.Errorf("the downloaded app's signature does not match the installed app's team, so it was not installed: %w", err)
	}
	return nil
}

// installedRequirement is the installed app's designated requirement, which
// for a Developer ID build names its identifier and its team.
func (p Plan) installedRequirement(ctx context.Context, env []string) (string, error) {
	cmd := exec.CommandContext(ctx, "codesign", "-d", "-r-", p.Root)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("could not read the signature of %s: %s", p.Root, lastLine(output, err))
	}
	for _, line := range strings.Split(string(output), "\n") {
		if requirement, ok := strings.CutPrefix(strings.TrimSpace(line), "designated => "); ok {
			if !strings.Contains(requirement, "subject.OU") {
				return "", fmt.Errorf("%s is not signed with a team ID (%s), so attn cannot check an update against it. Install the new version from the releases page", p.Root, requirement)
			}
			return requirement, nil
		}
	}
	return "", errors.New("codesign named no designated requirement for " + p.Root)
}
