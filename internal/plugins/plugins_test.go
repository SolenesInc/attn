package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadManifestAcceptsWhatARuntimeCanStartAndNamesWhatItRefuses(t *testing.T) {
	type file struct {
		body string
		mode os.FileMode
	}
	bunEntry := map[string]file{"src/index.ts": {"// entrypoint\n", 0o644}}
	cases := []struct {
		name     string
		manifest string
		files    map[string]file
		wantName string
		wantKind EntrypointKind
		wantPath string
		refusal  string
	}{
		{name: "a legacy entrypoint runs under bun",
			manifest: "name = \"worktree-provider\"\nversion = \"0.1.0\"\nattn_api_version = 7\n\n[plugin]\nentrypoint = \"src/index.ts\"\n",
			files:    bunEntry, wantName: "worktree-provider", wantKind: EntrypointBun, wantPath: "src/index.ts"},
		{name: "a runtime-only name may hold a slash",
			manifest: "name = \"team/worktree-provider\"\nversion = \"0.1.0\"\nattn_api_version = 7\n\n[plugin]\nentrypoint = \"src/index.ts\"\n",
			files:    bunEntry, wantName: "team/worktree-provider", wantKind: EntrypointBun, wantPath: "src/index.ts"},
		{name: "an executable entrypoint runs as itself",
			manifest: "name = \"provider\"\nversion = \"0.1.0\"\nattn_api_version = 7\n\n[plugin]\nkind = \"executable\"\npath = \"bin/provider\"\n",
			files:    map[string]file{"bin/provider": {"#!/bin/sh\n", 0o755}}, wantName: "provider", wantKind: EntrypointExecutable, wantPath: "bin/provider"},
		{name: "an executable entrypoint without the executable bit is refused",
			manifest: "name = \"provider\"\nversion = \"0.1.0\"\nattn_api_version = 7\n\n[plugin]\nkind = \"executable\"\npath = \"bin/provider\"\n",
			files:    map[string]file{"bin/provider": {"binary", 0o644}}, refusal: "must be executable"},
		{name: "an entrypoint outside the plugin is refused",
			manifest: "name = \"worktree-provider\"\nversion = \"0.1.0\"\nattn_api_version = 7\n\n[plugin]\nentrypoint = \"../outside.ts\"\n",
			files:    map[string]file{"../outside.ts": {"// outside\n", 0o644}}, refusal: "must stay within the plugin directory"},
		{name: "an older plugin API is refused",
			manifest: "name = \"provider\"\nversion = \"0.1.0\"\nattn_api_version = 6\n\n[plugin]\nentrypoint = \"entry.ts\"\n",
			files:    map[string]file{"entry.ts": {"// entrypoint\n", 0o644}}, refusal: "unsupported attn_api_version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "plugin")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			for rel, f := range tc.files {
				path := filepath.Join(root, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(f.body), f.mode); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, ManifestName), []byte(tc.manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			manifest, err := LoadManifest(filepath.Join(root, ManifestName))
			if tc.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refusal) {
					t.Fatalf("LoadManifest = %+v, %v; want a refusal naming %q", manifest, err, tc.refusal)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Name != tc.wantName || manifest.Plugin.Kind != tc.wantKind || manifest.Plugin.Path != tc.wantPath {
				t.Fatalf("LoadManifest = %q %+v, want %q running %s %s", manifest.Name, manifest.Plugin, tc.wantName, tc.wantKind, tc.wantPath)
			}
		})
	}
}
