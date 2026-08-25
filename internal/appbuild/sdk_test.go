package appbuild

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/docstore"
)

func materializeEnv(t *testing.T) (store, appDir string) {
	t.Helper()
	requireToolchain(t)
	root := t.TempDir()
	store = sharedToolchainStore(t, filepath.Join(root, "store"))
	appDir = filepath.Join(root, "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return store, appDir
}

func TestEnsureSDKMaterializesATypesOnlyPackage(t *testing.T) {
	store, appDir := materializeEnv(t)

	pkg, err := EnsureSDK(store, appDir, nil)
	if err != nil {
		t.Fatalf("EnsureSDK: %v", err)
	}

	if pkg != SDKDir(store, SDKHash()) {
		t.Fatalf("package dir = %s, want the hashed path %s", pkg, SDKDir(store, SDKHash()))
	}
	entries, err := os.ReadDir(pkg)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		if strings.HasSuffix(e.Name(), ".js") {
			t.Fatalf("the materialized package holds JavaScript (%s); it is types only", e.Name())
		}
	}
	for _, want := range []string{"package.json", "index.d.ts", "jsx-runtime.d.ts"} {
		if _, err := os.Stat(filepath.Join(pkg, want)); err != nil {
			t.Fatalf("no %s in the materialized package (%v); it holds %v", want, err, names)
		}
	}
}

func TestEnsureSDKLinksTheSpecifierAndReactsTypes(t *testing.T) {
	store, appDir := materializeEnv(t)

	pkg, err := EnsureSDK(store, appDir, nil)
	if err != nil {
		t.Fatalf("EnsureSDK: %v", err)
	}

	link := filepath.Join(appDir, filepath.FromSlash(SDKLinkPath))
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("reading %s: %v", link, err)
	}
	if target != pkg {
		t.Fatalf("%s points at %s, want %s", SDKLinkPath, target, pkg)
	}
	reactTypes := filepath.Join(pkg, "node_modules", "@types", "react", "index.d.ts")
	if _, err := os.Stat(reactTypes); err != nil {
		t.Fatalf("React's declarations do not resolve from the materialized package: %v", err)
	}
}

func TestEnsureSDKRepointsAStaleLink(t *testing.T) {
	store, appDir := materializeEnv(t)
	link := filepath.Join(appDir, filepath.FromSlash(SDKLinkPath))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(store, "sdk", "gone"), link); err != nil {
		t.Fatal(err)
	}

	pkg, err := EnsureSDK(store, appDir, nil)
	if err != nil {
		t.Fatalf("EnsureSDK: %v", err)
	}

	target, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if target != pkg {
		t.Fatalf("the stale link survived: %s", target)
	}
}

func TestEnsureSDKRefusesToReplaceARealDirectory(t *testing.T) {
	store, appDir := materializeEnv(t)
	link := filepath.Join(appDir, filepath.FromSlash(SDKLinkPath))
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := EnsureSDK(store, appDir, nil)

	if err == nil {
		t.Fatal("EnsureSDK replaced a real directory")
	}
	if !strings.Contains(err.Error(), "not attn's SDK link") {
		t.Fatalf("error does not say what is in the way: %v", err)
	}
	if _, statErr := os.Stat(link); statErr != nil {
		t.Fatalf("the directory was removed anyway: %v", statErr)
	}
}

func TestEnsureSDKRetiresA4sAmbientDeclaration(t *testing.T) {
	store, appDir := materializeEnv(t)
	legacy := filepath.Join(appDir, filepath.FromSlash(LegacySDKFile))
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(generatedHeader+"declare module \"x\" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var said []string

	if _, err := EnsureSDK(store, appDir, func(line string) { said = append(said, line) }); err != nil {
		t.Fatalf("EnsureSDK: %v", err)
	}

	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("%s survived (%v)", LegacySDKFile, err)
	}
	if !strings.Contains(strings.Join(said, "\n"), LegacySDKFile) {
		t.Fatalf("the removal was silent: %v", said)
	}
}

func TestEnsureSDKKeepsAnAmbientDeclarationAttnDidNotWrite(t *testing.T) {
	store, appDir := materializeEnv(t)
	legacy := filepath.Join(appDir, filepath.FromSlash(LegacySDKFile))
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("// mine\ndeclare module \"x\" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var said []string

	if _, err := EnsureSDK(store, appDir, func(line string) { said = append(said, line) }); err != nil {
		t.Fatalf("EnsureSDK: %v", err)
	}

	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("a file attn did not write was deleted: %v", err)
	}
	if !strings.Contains(strings.Join(said, "\n"), "delete it") {
		t.Fatalf("the reader was not told what to do: %v", said)
	}
}

func TestSDKDeclarationsMatchTheDaemonsContract(t *testing.T) {
	index := SDKFiles()["index.d.ts"]
	if index == "" {
		t.Fatal("no index.d.ts is embedded")
	}

	wantAPI := regexp.MustCompile(`export declare const APP_API_VERSION = (\d+);`).FindStringSubmatch(index)
	if wantAPI == nil {
		t.Fatal("index.d.ts declares no APP_API_VERSION")
	}
	if got := wantAPI[1]; got != strconv.Itoa(APIVersion) {
		t.Fatalf("the SDK says app api %s, this attn speaks %d", got, APIVersion)
	}

	var ops []string
	for _, op := range docstore.FilterOps() {
		ops = append(ops, "\""+string(op)+"\"")
	}
	want := "export type FilterOp = " + strings.Join(ops, " | ") + ";"
	if !strings.Contains(index, want) {
		t.Fatalf("the SDK's FilterOp union is not the store's operator set; expected the line\n  %s", want)
	}
}

func TestBothManifestsExportTheSameSpecifiers(t *testing.T) {
	exports := func(what string, data []byte) []string {
		t.Helper()
		var manifest struct {
			Exports map[string]any `json:"exports"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			t.Fatalf("parsing %s: %v", what, err)
		}
		if len(manifest.Exports) == 0 {
			t.Fatalf("%s declares no exports", what)
		}
		var keys []string
		for key := range manifest.Exports {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return keys
	}

	source, err := os.ReadFile(filepath.Join("..", "..", "sdk", "attn-app", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := exports("sdk/attn-app/package.json", source)
	got := exports("the materialized manifest", []byte(sdkPackageJSON))
	if !slices.Equal(got, want) {
		t.Fatalf("the materialized package exports %v, the workspace package exports %v", got, want)
	}

	files := SDKFiles()
	for _, specifier := range got {
		name := "index.d.ts"
		if specifier != "." {
			name = strings.TrimPrefix(specifier, "./") + ".d.ts"
		}
		if _, ok := files[name]; !ok {
			t.Fatalf("the SDK exports %s but ships no %s; run make generate-sdk", specifier, name)
		}
	}
}

func TestReactTypesPinMatchesTheFrontend(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "app", "pnpm-lock.yaml"))
	if err != nil {
		t.Fatalf("reading the frontend lockfile: %v", err)
	}
	found := directDependencyVersions(string(data), "@types/react")
	if len(found) == 0 {
		t.Fatal("the frontend lockfile declares no direct @types/react")
	}
	for _, version := range found {
		if version != ReactTypesVersion {
			t.Fatalf("the frontend resolves @types/react %s and the toolchain pins %s; an app would typecheck against declarations the frontend does not run. Update ReactTypesVersion.",
				version, ReactTypesVersion)
		}
	}
}

// Direct importer entries only, matching the pnpm lockfile's specifier/version shape.
func directDependencyVersions(lock, pkg string) []string {
	var out []string
	lines := strings.Split(lock, "\n")
	head := regexp.MustCompile(`^\s+'?` + regexp.QuoteMeta(pkg) + `'?:$`)
	version := regexp.MustCompile(`^\s+version: (\S+)$`)
	for i, line := range lines {
		if !head.MatchString(line) || i+2 >= len(lines) {
			continue
		}
		if !strings.Contains(lines[i+1], "specifier:") {
			continue
		}
		if m := version.FindStringSubmatch(lines[i+2]); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}
