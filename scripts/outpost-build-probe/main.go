package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/outpostbuild"
)

func main() {
	home := flag.String("home", "", "home binary whose build is acquired")
	repository := flag.String("repository", "", "recorded source repository")
	cache := flag.String("cache", "", "isolated outpost-builds directory")
	platform := flag.String("platform", "linux/amd64", "outpost platform")
	releaseSource := flag.String("release-source", "https://github.com/victorarias/attn/releases/download", "release download source")
	flag.Parse()
	if *home == "" || *cache == "" {
		fail(fmt.Errorf("--home and --cache are required"))
	}
	read := func(args ...string) []byte {
		out, err := exec.Command(*home, args...).Output()
		if err != nil {
			fail(err)
		}
		return out
	}
	var info struct {
		Version     string `json:"version"`
		Channel     string `json:"channel"`
		Fingerprint string `json:"sourceFingerprint"`
	}
	if err := json.Unmarshal(read("--build-info-json"), &info); err != nil {
		fail(err)
	}
	build := bridge.Build{Version: info.Version, SourceFingerprint: info.Fingerprint, ProtocolVersion: strings.TrimSpace(string(read("--protocol-version")))}
	start := time.Now()
	path, err := outpostbuild.Acquire(context.Background(), outpostbuild.Request{Build: build, Channel: info.Channel, Repository: *repository, ReleaseSource: *releaseSource, Cache: *cache, Platform: *platform}, func(phase string) { fmt.Fprintln(os.Stderr, phase) })
	if err != nil {
		fail(err)
	}
	duration := time.Since(start)
	file, err := os.Open(path)
	if err != nil {
		fail(err)
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		fail(err)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"build": build, "channel": info.Channel, "platform": *platform, "path": path, "seconds": duration.Seconds(), "size": size, "sha256": hex.EncodeToString(hash.Sum(nil))})
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
