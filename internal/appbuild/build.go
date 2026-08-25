package appbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Options struct {
	Dir      string
	StoreDir string
	Log      func(string)
}

type Result struct {
	Manifest        Manifest
	Declaration     string
	ContentHash     string
	ArtifactPath    string
	ArtifactWritten bool
	BundleBytes     int64
	ViewBytes       []ViewSize
}

type ViewSize struct {
	Name  string
	Path  string
	Bytes int64
}

const ArtifactName = "bundle.js"

const viewsDirName = "views"

func ShortHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func ArtifactDir(storeDir, name, hash string) string {
	return filepath.Join(storeDir, name, hash)
}

func ArtifactPath(storeDir, name, hash string) string {
	return filepath.Join(ArtifactDir(storeDir, name, hash), ArtifactName)
}

func ViewArtifactPath(storeDir, name, hash, view string) string {
	return filepath.Join(ArtifactDir(storeDir, name, hash), viewsDirName, view+".js")
}

func Build(ctx context.Context, opts Options) (Result, error) {
	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return Result{}, fmt.Errorf("resolving app directory %s: %w", opts.Dir, err)
	}
	manifest, err := LoadManifest(dir)
	if err != nil {
		return Result{}, err
	}
	declaration, err := manifest.Declaration()
	if err != nil {
		return Result{}, err
	}
	if err := WriteGenerated(dir, manifest); err != nil {
		return Result{}, err
	}
	tools, err := ResolveToolchain(opts.StoreDir, opts.Log)
	if err != nil {
		return Result{}, err
	}
	if _, err := EnsureSDK(opts.StoreDir, dir, opts.Log); err != nil {
		return Result{}, err
	}

	logf(opts.Log, "typechecking %s", strings.Join(append([]string{manifest.Entrypoint}, viewEntrypoints(manifest)...), ", "))
	if err := typecheck(ctx, tools, dir, manifest); err != nil {
		return Result{}, err
	}

	// Staging lives inside the store: the last step is a rename into it, and a
	// cross-filesystem rename fails — /tmp is a different volume often on Linux.
	stagingRoot := filepath.Join(opts.StoreDir, ".staging")
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return Result{}, fmt.Errorf("creating the app build staging directory %s: %w", stagingRoot, err)
	}
	staging, err := os.MkdirTemp(stagingRoot, manifest.Name+"-")
	if err != nil {
		return Result{}, fmt.Errorf("creating a build directory under %s: %w", stagingRoot, err)
	}
	defer os.RemoveAll(staging)

	logf(opts.Log, "bundling %s", manifest.Entrypoint)
	bundle := filepath.Join(staging, ArtifactName)
	if err := bundleApp(ctx, tools, dir, manifest, bundle); err != nil {
		return Result{}, err
	}
	built, err := os.ReadFile(bundle)
	if err != nil {
		return Result{}, fmt.Errorf("reading the built bundle: %w", err)
	}

	views := make([]ViewArtifact, 0, len(manifest.Views))
	for _, v := range manifest.Views {
		logf(opts.Log, "bundling view %s from %s", v.Name, v.Entrypoint)
		out := filepath.Join(staging, viewsDirName, v.Name+".js")
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return Result{}, fmt.Errorf("creating the view build directory %s: %w", filepath.Dir(out), err)
		}
		if err := bundleView(ctx, tools, dir, manifest, v, out); err != nil {
			return Result{}, err
		}
		content, err := os.ReadFile(out)
		if err != nil {
			return Result{}, fmt.Errorf("reading the built view %q: %w", v.Name, err)
		}
		views = append(views, ViewArtifact{Name: v.Name, Content: content})
	}

	hash := versionHash(declaration, built, views)
	path, written, err := placeArtifact(opts.StoreDir, manifest.Name, hash, staging)
	if err != nil {
		return Result{}, err
	}
	sizes := make([]ViewSize, 0, len(views))
	for _, v := range views {
		sizes = append(sizes, ViewSize{
			Name:  v.Name,
			Path:  ViewArtifactPath(opts.StoreDir, manifest.Name, hash, v.Name),
			Bytes: int64(len(v.Content)),
		})
	}
	return Result{
		Manifest:        manifest,
		Declaration:     declaration,
		ContentHash:     hash,
		ArtifactPath:    path,
		ArtifactWritten: written,
		BundleBytes:     int64(len(built)),
		ViewBytes:       sizes,
	}, nil
}

func WriteGenerated(dir string, m Manifest) error {
	files := map[string]string{
		GeneratedFile: GenerateTypes(m),
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
		}
		// Rewriting an unchanged file touches its mtime, and `attn app dev` watches
		// this directory: codegen would wake the watcher that triggered it, forever.
		if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return nil
}

func typecheck(ctx context.Context, tools Toolchain, dir string, m Manifest) error {
	args := []string{
		"--noEmit",
		"--strict",
		"--target", "es2022",
		"--module", "esnext",
		"--moduleResolution", "bundler",
		"--skipLibCheck",
		"--pretty", "false",
		"--jsx", "react-jsx",
		"--jsxImportSource", SDKModule,
		filepath.FromSlash(m.Entrypoint),
	}
	for _, v := range m.Views {
		args = append(args, filepath.FromSlash(v.Entrypoint))
	}
	cmd := exec.CommandContext(ctx, tools.TSC, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return fmt.Errorf("typechecking app %q failed with no output (%v)", m.Name, err)
	}
	return fmt.Errorf("app %q does not typecheck against its manifest:\n%s", m.Name, text)
}

func viewEntrypoints(m Manifest) []string {
	out := make([]string, 0, len(m.Views))
	for _, v := range m.Views {
		out = append(out, v.Entrypoint)
	}
	return out
}

func bundleApp(ctx context.Context, tools Toolchain, dir string, m Manifest, outfile string) error {
	cmd := exec.CommandContext(ctx, tools.Bun, "build",
		filepath.FromSlash(m.Entrypoint),
		"--target", "bun",
		"--format", "esm",
		"--outfile", outfile,
	)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bundling app %q failed:\n%s", m.Name, strings.TrimSpace(string(out)))
	}
	return nil
}

type ViewArtifact struct {
	Name    string
	Content []byte
}

// `--production` is NOT optional. Measured: without it bun emits jsxDEV imports
// whatever tsconfig says, and React's production build exports jsxDEV undefined.
func bundleView(ctx context.Context, tools Toolchain, dir string, m Manifest, v View, outfile string) error {
	args := []string{"build", filepath.FromSlash(v.Entrypoint), "--target", "browser", "--format", "esm", "--production"}
	for _, specifier := range SDKSpecifiers() {
		args = append(args, "--external", specifier)
	}
	args = append(args, "--outfile", outfile)
	cmd := exec.CommandContext(ctx, tools.Bun, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bundling view %q of app %q failed:\n%s", v.Name, m.Name, strings.TrimSpace(string(out)))
	}
	return nil
}

// The handler bundle alone would be wrong twice: a manifest edit can leave it
// byte-identical, and editing only a view moves neither it nor the declaration.
func versionHash(declaration string, bundle []byte, views []ViewArtifact) string {
	h := sha256.New()
	h.Write([]byte(declaration))
	h.Write([]byte{0})
	h.Write(bundle)
	ordered := append([]ViewArtifact(nil), views...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	for _, v := range ordered {
		h.Write([]byte{0})
		h.Write([]byte(v.Name))
		h.Write([]byte{0})
		h.Write(v.Content)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func VersionHash(declaration string, bundle []byte, views []ViewArtifact) string {
	return versionHash(declaration, bundle, views)
}

func ReadViewArtifacts(storeDir, name, hash string, views []string) ([]ViewArtifact, error) {
	out := make([]ViewArtifact, 0, len(views))
	for _, view := range views {
		path := ViewArtifactPath(storeDir, name, hash, view)
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading the built view %q of app %q at %s: %w", view, name, path, err)
		}
		out = append(out, ViewArtifact{Name: view, Content: content})
	}
	return out, nil
}

func placeArtifact(storeDir, name, hash, staging string) (string, bool, error) {
	target := ArtifactDir(storeDir, name, hash)
	final := filepath.Join(target, ArtifactName)
	if _, err := os.Stat(final); err == nil {
		return final, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", false, fmt.Errorf("creating the app artifact store %s: %w", filepath.Dir(target), err)
	}
	if err := os.Rename(staging, target); err != nil {
		// Another apply of the same content won the race; its directory holds the
		// same bytes by construction, so the loser has nothing to do.
		if _, statErr := os.Stat(final); statErr == nil {
			return final, false, nil
		}
		return "", false, fmt.Errorf("placing the artifact of app %q at %s: %w", name, target, err)
	}
	return final, true, nil
}

func logf(log func(string), format string, args ...any) {
	if log != nil {
		log(fmt.Sprintf(format, args...))
	}
}
