package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
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
	root, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return testhistory.Revision{}
	}
	ids, err := gitIn(root, nil, "rev-parse", "HEAD", "HEAD^{tree}")
	fields := strings.Fields(string(ids))
	if err != nil || len(fields) != 2 {
		return testhistory.Revision{}
	}
	return testhistory.Revision{Commit: fields[0], Tree: fields[1], DirtyFingerprint: dirtyFingerprint(root)}
}

const unfingerprintedChanges = "unknown"

func dirtyFingerprint(root string) string {
	status, err := gitIn(root, nil, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return unfingerprintedChanges
	}
	if len(bytes.TrimSpace(status)) == 0 {
		return ""
	}
	diff, err := gitIn(root, nil, "diff", "--binary", "HEAD")
	if err != nil {
		return unfingerprintedChanges
	}
	hash := sha256.New()
	hash.Write(diff)
	untracked, err := gitIn(root, nil, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return unfingerprintedChanges
	}
	if paths := strings.TrimSpace(string(untracked)); paths != "" {
		ids, err := gitIn(root, strings.NewReader(paths+"\n"), "hash-object", "--stdin-paths")
		if err != nil {
			return unfingerprintedChanges
		}
		hash.Write([]byte{0})
		hash.Write([]byte(paths))
		hash.Write([]byte{0})
		hash.Write(ids)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func gitIn(dir string, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Stdin = dir, stdin
	return cmd.Output()
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
