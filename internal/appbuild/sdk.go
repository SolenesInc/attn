package appbuild

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Design: docs/plans/2026-08-13-ext-a5-ui-host-and-app-sdk.md, "SDK packaging".

//go:embed sdkdist
var sdkDeclarations embed.FS

const (
	sdkDistDir = "sdkdist"

	sdkDirName = "sdk"

	SDKLinkPath = "node_modules/" + SDKModule

	LegacySDKFile = "src/attn-app.d.ts"
)

const sdkPackageJSON = `{
  "name": "` + SDKModule + `",
  "version": "0.0.0",
  "private": true,
  "type": "module",
  "types": "./index.d.ts",
  "exports": {
    ".": { "types": "./index.d.ts" },
    "./jsx-runtime": { "types": "./jsx-runtime.d.ts" },
    "./jsx-dev-runtime": { "types": "./jsx-dev-runtime.d.ts" }
  }
}
`

func SDKFiles() map[string]string {
	files := map[string]string{"package.json": sdkPackageJSON}
	entries, err := fs.ReadDir(sdkDeclarations, sdkDistDir)
	if err != nil {
		panic(fmt.Sprintf("the embedded app SDK declarations are unreadable: %v", err))
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := sdkDeclarations.ReadFile(sdkDistDir + "/" + entry.Name())
		if err != nil {
			panic(fmt.Sprintf("reading the embedded app SDK file %s: %v", entry.Name(), err))
		}
		files[entry.Name()] = string(data)
	}
	return files
}

func SDKHash() string {
	files := SDKFiles()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write([]byte(files[name]))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func SDKDir(storeDir, hash string) string {
	return filepath.Join(storeDir, sdkDirName, hash)
}

func EnsureSDK(storeDir, appDir string, log func(string)) (string, error) {
	pkg, err := materializeSDK(storeDir)
	if err != nil {
		return "", err
	}
	if err := linkSDK(appDir, pkg); err != nil {
		return "", err
	}
	retireLegacySDKFile(appDir, log)
	return pkg, nil
}

// Runs under the toolchain lock: the package it writes points at the toolchain's
// node_modules for React's types.
func materializeSDK(storeDir string) (string, error) {
	root := filepath.Join(storeDir, sdkDirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("creating the app SDK directory %s: %w", root, err)
	}
	toolchain := filepath.Join(storeDir, toolchainDirName)
	if err := os.MkdirAll(toolchain, 0o755); err != nil {
		return "", fmt.Errorf("creating the app toolchain directory %s: %w", toolchain, err)
	}
	unlock, err := lockDir(toolchain)
	if err != nil {
		return "", err
	}
	defer unlock()

	target := SDKDir(storeDir, SDKHash())
	if _, err := os.Stat(filepath.Join(target, "package.json")); err == nil {
		return target, linkSDKTypes(target)
	}

	staging, err := os.MkdirTemp(root, ".staging-")
	if err != nil {
		return "", fmt.Errorf("creating a staging directory under %s: %w", root, err)
	}
	defer os.RemoveAll(staging)
	for name, content := range SDKFiles() {
		if err := os.WriteFile(filepath.Join(staging, name), []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("writing the app SDK file %s: %w", name, err)
		}
	}
	if err := linkSDKTypes(staging); err != nil {
		return "", err
	}
	if err := os.Rename(staging, target); err != nil {
		if _, statErr := os.Stat(filepath.Join(target, "package.json")); statErr == nil {
			return target, linkSDKTypes(target)
		}
		return "", fmt.Errorf("placing the app SDK at %s: %w", target, err)
	}
	return target, nil
}

func linkSDKTypes(pkgDir string) error {
	link := filepath.Join(pkgDir, "node_modules")
	const target = "../../toolchain/node_modules"
	if existing, err := os.Readlink(link); err == nil {
		if existing == target {
			return nil
		}
	} else if _, statErr := os.Lstat(link); statErr == nil {
		return fmt.Errorf("%s is not a symlink, and the app SDK owns it; remove it and re-apply", link)
	}
	_ = os.Remove(link)
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("linking the app SDK at %s to the toolchain's types: %w", link, err)
	}
	return nil
}

// A real directory at the link path is never removed: it is the author's own install.
func linkSDK(appDir, pkgDir string) error {
	link := filepath.Join(appDir, filepath.FromSlash(SDKLinkPath))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(link), err)
	}
	if existing, err := os.Readlink(link); err == nil {
		if existing == pkgDir {
			return nil
		}
	} else if info, statErr := os.Lstat(link); statErr == nil {
		return fmt.Errorf("%s already exists and is not attn's SDK link (%s); remove it and re-apply, or move the app out of a tree that installs %s",
			link, describeEntry(info), SDKModule)
	}
	_ = os.Remove(link)
	if err := os.Symlink(pkgDir, link); err != nil {
		return fmt.Errorf("linking %s into %s: %w", SDKModule, link, err)
	}
	return nil
}

func describeEntry(info os.FileInfo) string {
	if info.IsDir() {
		return "a directory"
	}
	return "a file"
}

// attn's own do-not-edit header is the whole test: a file under this name that
// attn did not write is left alone and reported.
func retireLegacySDKFile(appDir string, log func(string)) {
	path := filepath.Join(appDir, filepath.FromSlash(LegacySDKFile))
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if !strings.HasPrefix(string(data), generatedHeader) {
		logf(log, "%s is not attn's generated file, so it was kept — it now declares %s a second time, and the app will typecheck against whichever one wins; delete it",
			LegacySDKFile, SDKModule)
		return
	}
	if err := os.Remove(path); err != nil {
		logf(log, "could not remove %s (%v); it now declares %s a second time and should be deleted by hand", LegacySDKFile, err, SDKModule)
		return
	}
	logf(log, "removed %s — the SDK is a package now, linked at %s", LegacySDKFile, SDKLinkPath)
}
