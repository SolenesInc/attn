package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/victorarias/attn/internal/testhistory"
)

func spoolDir() (string, error) {
	if dir := os.Getenv("ATTN_TEST_HISTORY_DIR"); dir != "" {
		return filepath.Abs(dir)
	}
	common, err := git("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("no spool: set ATTN_TEST_HISTORY_DIR or run inside a Git checkout (%v)", err)
	}
	return filepath.Join(common, "attn-test-history"), nil
}

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return strings.TrimSpace(string(out)), err
}

func repository() string {
	if repo := os.Getenv("GITHUB_REPOSITORY"); repo != "" {
		return "github.com/" + repo
	}
	url, err := git("remote", "get-url", "origin")
	if err != nil || url == "" {
		return "unknown"
	}
	url = strings.TrimSuffix(url, ".git")
	if rest, ok := strings.CutPrefix(url, "git@"); ok {
		return strings.Replace(rest, ":", "/", 1)
	}
	for _, scheme := range []string{"https://", "http://", "ssh://git@", "ssh://"} {
		if rest, ok := strings.CutPrefix(url, scheme); ok {
			return rest
		}
	}
	return url
}

func revision() testhistory.Revision {
	ids, err := git("rev-parse", "HEAD", "HEAD^{tree}")
	fields := strings.Fields(ids)
	if err != nil || len(fields) != 2 {
		return testhistory.Revision{}
	}
	return testhistory.Revision{Commit: fields[0], Tree: fields[1], DirtyFingerprint: dirtyFingerprint()}
}

func dirtyFingerprint() string {
	status, err := exec.Command("git", "status", "--porcelain", "--untracked-files=all").Output()
	if err != nil || len(bytes.TrimSpace(status)) == 0 {
		return ""
	}
	hash := sha256.New()
	diff, err := exec.Command("git", "diff", "--binary", "HEAD").Output()
	if err != nil {
		return ""
	}
	hash.Write(diff)
	untracked, err := exec.Command("git", "ls-files", "--others", "--exclude-standard").Output()
	if err != nil {
		return ""
	}
	if paths := strings.TrimSpace(string(untracked)); paths != "" {
		blobs := exec.Command("git", "hash-object", "--stdin-paths")
		blobs.Stdin = strings.NewReader(paths + "\n")
		ids, err := blobs.Output()
		if err != nil {
			return ""
		}
		hash.Write([]byte{0})
		hash.Write([]byte(paths))
		hash.Write([]byte{0})
		hash.Write(ids)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func machineClass() string {
	host := "local"
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		host = "github-hosted"
	}
	class := fmt.Sprintf("%s %dcpu", host, runtime.NumCPU())
	if bytes := memoryBytes(); bytes > 0 {
		class += fmt.Sprintf("/%dGB", (bytes+(1<<29))>>30)
	}
	return class
}
