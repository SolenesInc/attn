package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

const defaultManifestPath = ".github/release-candidate.yml"

var (
	plainVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	commitPattern       = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
	jsonVersionPattern  = regexp.MustCompile(`(?m)("version"\s*:\s*")([^"]+)(")`)
	cargoVersionPattern = regexp.MustCompile(`(?m)^(version\s*=\s*")([^"]+)("\s*)$`)
	cargoLockAppPattern = regexp.MustCompile(`(?m)(\[\[package\]\]\nname = "app"\nversion = ")([^"]+)(")`)
)

type candidateManifest struct {
	Version string `yaml:"version"`
	MainSHA string `yaml:"main_sha"`
}

type versionSource struct {
	path string
	read func([]byte) (string, error)
	set  func([]byte, string) ([]byte, error)
}

type candidateValidation struct {
	manifestPath        string
	currentMainRef      string
	headRef             string
	tagStatus           string
	otherOpenCandidates int
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "release-train:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError()
	}
	root, err := repositoryRoot()
	if err != nil {
		return err
	}
	switch args[0] {
	case "version":
		return runVersion(root, args[1:], stdout)
	case "fragments":
		return runFragments(root, args[1:], stdout)
	case "manifest":
		return runManifest(root, args[1:], stdout)
	case "candidate":
		return runCandidate(root, args[1:], stdout)
	case "accepted-main":
		return runAcceptedMain(root, args[1:], stdout)
	case "help", "--help", "-h":
		_, err := fmt.Fprint(stdout, usage())
		return err
	default:
		return usageError()
	}
}

func usage() string {
	return `usage: release-train <command>

commands:
  version set <vX.Y.Z>       update every committed version source
  version check <vX.Y.Z>     require every version source to agree
  fragments render           render pending fragments for the release writer
  fragments receipt          print the exact pending-fragment audit receipt
  manifest write [flags]     write .github/release-candidate.yml
  candidate validate [flags] validate a prepared release candidate
  accepted-main tag          print the manifest's release tag for main
  accepted-main validate     validate and print the release tag for main
`
}

func usageError() error {
	return errors.New(strings.TrimSpace(usage()))
}

func repositoryRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("find repository root: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func runVersion(root string, args []string, stdout io.Writer) error {
	if len(args) != 2 || (args[0] != "set" && args[0] != "check") {
		return errors.New("usage: release-train version <set|check> <vX.Y.Z>")
	}
	version, _, err := normalizeVersion(args[1])
	if err != nil {
		return err
	}
	if args[0] == "set" {
		if err := setVersions(root, version); err != nil {
			return err
		}
	}
	if err := checkVersions(root, "", version); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "all release versions are %s\n", version)
	return err
}

func versionSources() []versionSource {
	return []versionSource{
		{path: "app/package.json", read: readJSONVersion, set: setJSONVersion},
		{path: "app/src-tauri/tauri.conf.json", read: readJSONVersion, set: setJSONVersion},
		{path: "app/src-tauri/Cargo.toml", read: readCargoVersion, set: setCargoVersion},
		{path: "app/src-tauri/Cargo.lock", read: readCargoLockVersion, set: setCargoLockVersion},
	}
}

func setVersions(root, version string) error {
	versions, err := readVersions(root, "")
	if err != nil {
		return err
	}
	if err := requireAgreement(versions, ""); err != nil {
		return fmt.Errorf("refuse to update inconsistent versions: %w", err)
	}
	updatedFiles := make(map[string][]byte, len(versionSources()))
	for _, source := range versionSources() {
		path := filepath.Join(root, source.path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		updated, err := source.set(data, version)
		if err != nil {
			return fmt.Errorf("%s: %w", source.path, err)
		}
		updatedFiles[path] = updated
	}
	for path, data := range updatedFiles {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func checkVersions(root, ref, expected string) error {
	versions, err := readVersions(root, ref)
	if err != nil {
		return err
	}
	if err := requireAgreement(versions, expected); err != nil {
		return err
	}
	return nil
}

func readVersions(root, ref string) (map[string]string, error) {
	versions := make(map[string]string, len(versionSources()))
	for _, source := range versionSources() {
		data, err := readRepositoryFile(root, ref, source.path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source.path, err)
		}
		version, err := source.read(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source.path, err)
		}
		versions[source.path] = version
	}
	return versions, nil
}

func requireAgreement(versions map[string]string, expected string) error {
	paths := make([]string, 0, len(versions))
	for path := range versions {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var first string
	for _, path := range paths {
		version := versions[path]
		if first == "" {
			first = version
		}
		if version != first {
			return fmt.Errorf("version disagreement: %s has %s, expected %s", path, version, first)
		}
		if expected != "" && version != expected {
			return fmt.Errorf("%s has version %s, expected %s", path, version, expected)
		}
	}
	return nil
}

func readRepositoryFile(root, ref, path string) ([]byte, error) {
	if ref == "" {
		return os.ReadFile(filepath.Join(root, path))
	}
	return gitOutput(root, "show", ref+":"+path)
}

func readJSONVersion(data []byte) (string, error) {
	matches := jsonVersionPattern.FindAllSubmatch(data, -1)
	if len(matches) != 1 {
		return "", fmt.Errorf("expected one version field, found %d", len(matches))
	}
	return string(matches[0][2]), nil
}

func setJSONVersion(data []byte, version string) ([]byte, error) {
	return replaceVersion(data, jsonVersionPattern, version)
}

func readCargoVersion(data []byte) (string, error) {
	var manifest struct {
		Package struct {
			Version string
		}
	}
	if _, err := toml.Decode(string(data), &manifest); err != nil {
		return "", err
	}
	if manifest.Package.Version == "" {
		return "", errors.New("package version is missing")
	}
	return manifest.Package.Version, nil
}

func setCargoVersion(data []byte, version string) ([]byte, error) {
	return replaceVersion(data, cargoVersionPattern, version)
}

func readCargoLockVersion(data []byte) (string, error) {
	var lock struct {
		Package []struct {
			Name    string
			Version string
		}
	}
	if _, err := toml.Decode(string(data), &lock); err != nil {
		return "", err
	}
	for _, pkg := range lock.Package {
		if pkg.Name == "app" {
			return pkg.Version, nil
		}
	}
	return "", errors.New("app package is missing")
}

func setCargoLockVersion(data []byte, version string) ([]byte, error) {
	return replaceVersion(data, cargoLockAppPattern, version)
}

func replaceVersion(data []byte, pattern *regexp.Regexp, version string) ([]byte, error) {
	matches := pattern.FindAllSubmatchIndex(data, -1)
	if len(matches) != 1 {
		return nil, fmt.Errorf("expected one version field, found %d", len(matches))
	}
	match := matches[0]
	var out bytes.Buffer
	out.Write(data[:match[4]])
	out.WriteString(version)
	out.Write(data[match[5]:])
	return out.Bytes(), nil
}

func runFragments(root string, args []string, stdout io.Writer) error {
	if len(args) != 1 || (args[0] != "render" && args[0] != "receipt") {
		return errors.New("usage: release-train fragments <render|receipt>")
	}
	if args[0] == "receipt" {
		receipt, err := fragmentReceipt(root, "HEAD")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, receipt)
		return err
	}
	rendered, err := renderFragments(root)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, rendered)
	return err
}

func renderFragments(root string) (string, error) {
	dir := filepath.Join(root, "changelog.d")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".yaml") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	var out strings.Builder
	for _, name := range names {
		path := filepath.Join("changelog.d", name)
		subjectBytes, err := gitOutput(root, "log", "--diff-filter=A", "--format=%s", "-1", "--", path)
		if err != nil {
			return "", err
		}
		subject := strings.TrimSpace(string(subjectBytes))
		if subject == "" {
			subject = "(uncommitted)"
		}
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&out, "--- fragment: %s\n--- introduced by: %s\n%s", path, subject, data)
		if len(data) == 0 || data[len(data)-1] != '\n' {
			out.WriteByte('\n')
		}
		out.WriteByte('\n')
	}
	return out.String(), nil
}

func runManifest(root string, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "write" {
		return errors.New("usage: release-train manifest write --version vX.Y.Z --main <ref> [--output path]")
	}
	flags := flag.NewFlagSet("manifest write", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	versionFlag := flags.String("version", "", "release version")
	mainRef := flags.String("main", "", "main ref at candidate creation")
	output := flags.String("output", defaultManifestPath, "manifest path")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	version, _, err := normalizeVersion(*versionFlag)
	if err != nil {
		return err
	}
	mainSHA, err := resolveCommit(root, *mainRef)
	if err != nil {
		return fmt.Errorf("main: %w", err)
	}
	manifest := candidateManifest{Version: version, MainSHA: mainSHA}
	if err := validateManifest(manifest); err != nil {
		return err
	}
	path := filepath.Join(root, *output)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "wrote %s for v%s on main %s\n", *output, version, mainSHA)
	return err
}

func readManifest(path string) (candidateManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return candidateManifest{}, err
	}
	return decodeManifest(data)
}

func readManifestAtRef(root, ref, path string) (candidateManifest, error) {
	data, err := readRepositoryFile(root, ref, path)
	if err != nil {
		return candidateManifest{}, err
	}
	return decodeManifest(data)
}

func decodeManifest(data []byte) (candidateManifest, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var manifest candidateManifest
	if err := decoder.Decode(&manifest); err != nil {
		return candidateManifest{}, err
	}
	var extra candidateManifest
	if err := decoder.Decode(&extra); err == nil {
		return candidateManifest{}, errors.New("manifest holds more than one YAML document")
	} else if !errors.Is(err, io.EOF) {
		return candidateManifest{}, err
	}
	if err := validateManifest(manifest); err != nil {
		return candidateManifest{}, err
	}
	return manifest, nil
}

func validateManifest(manifest candidateManifest) error {
	if !plainVersionPattern.MatchString(manifest.Version) {
		return fmt.Errorf("version must look like 1.2.3 (got %q)", manifest.Version)
	}
	if !commitPattern.MatchString(manifest.MainSHA) {
		return errors.New("main_sha must be a full commit SHA")
	}
	return nil
}

func runCandidate(root string, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "validate" {
		return errors.New("usage: release-train candidate validate --current-main <ref> --tag-status absent --other-open-candidates 0 [--head ref] [--manifest path]")
	}
	flags := flag.NewFlagSet("candidate validate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	input := candidateValidation{headRef: "HEAD", manifestPath: defaultManifestPath, otherOpenCandidates: -1}
	flags.StringVar(&input.manifestPath, "manifest", input.manifestPath, "candidate manifest")
	flags.StringVar(&input.currentMainRef, "current-main", "", "current main ref")
	flags.StringVar(&input.headRef, "head", input.headRef, "candidate head ref")
	flags.StringVar(&input.tagStatus, "tag-status", "", "remote candidate tag status")
	flags.IntVar(&input.otherOpenCandidates, "other-open-candidates", -1, "other open candidate count")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if err := validateCandidate(root, input); err != nil {
		return err
	}
	manifest, err := readManifest(filepath.Join(root, input.manifestPath))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "candidate on main %s is valid for v%s\n", manifest.MainSHA, manifest.Version)
	return err
}

func validateCandidate(root string, input candidateValidation) error {
	headSHA, err := resolveCommit(root, input.headRef)
	if err != nil {
		return fmt.Errorf("head: %w", err)
	}
	manifest, err := readManifestAtRef(root, headSHA, input.manifestPath)
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if input.otherOpenCandidates < 0 {
		return errors.New("other open candidate count is missing")
	}
	if input.otherOpenCandidates != 0 {
		return fmt.Errorf("found %d other open release candidate(s)", input.otherOpenCandidates)
	}
	if strings.ToLower(input.tagStatus) != "absent" {
		if input.tagStatus == "" {
			return errors.New("remote tag status is missing")
		}
		return fmt.Errorf("remote tag status is %s, expected absent", input.tagStatus)
	}
	if input.currentMainRef == "" {
		return errors.New("current main ref is required")
	}
	currentMainSHA, err := resolveCommit(root, input.currentMainRef)
	if err != nil {
		return fmt.Errorf("current main: %w", err)
	}
	if err := requireAncestor(root, manifest.MainSHA, currentMainSHA, "recorded main is not on current main"); err != nil {
		return err
	}
	if err := requireAncestor(root, manifest.MainSHA, headSHA, "recorded main is not an ancestor of the candidate head"); err != nil {
		return err
	}
	if err := checkVersions(root, headSHA, manifest.Version); err != nil {
		return err
	}
	if err := requireNoFragments(root, headSHA); err != nil {
		return err
	}
	if err := requireCompiledChangelog(root, manifest.MainSHA, headSHA); err != nil {
		return err
	}
	if err := requireReleaseOnlyChanges(root, manifest.MainSHA, headSHA, input.manifestPath); err != nil {
		return err
	}
	return nil
}

func requireNoFragments(root, ref string) error {
	out, err := gitOutput(root, "ls-tree", "-r", "--name-only", ref, "--", "changelog.d")
	if err != nil {
		return err
	}
	var fragments []string
	for _, path := range strings.Fields(string(out)) {
		if strings.HasSuffix(path, ".yaml") {
			fragments = append(fragments, path)
		}
	}
	if len(fragments) > 0 {
		return fmt.Errorf("candidate still contains pending changelog fragments: %s", strings.Join(fragments, ", "))
	}
	return nil
}

func requireReleasedFragmentsConsumed(root, mainSHA, headSHA string) error {
	released, err := fragmentBlobs(root, mainSHA)
	if err != nil {
		return err
	}
	current, err := fragmentBlobs(root, headSHA)
	if err != nil {
		return err
	}
	var remaining []string
	for path := range released {
		if _, ok := current[path]; ok {
			remaining = append(remaining, path)
		}
	}
	sort.Strings(remaining)
	if len(remaining) > 0 {
		return fmt.Errorf("release still contains the changelog fragments it compiled: %s", strings.Join(remaining, ", "))
	}
	return nil
}

func requireCompiledChangelog(root, source, head string) error {
	fragments, err := fragmentBlobs(root, source)
	if err != nil {
		return err
	}
	userFacing := false
	for path := range fragments {
		data, err := readRepositoryFile(root, source, path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		var fragment struct {
			Kind string `yaml:"kind"`
		}
		if err := yaml.Unmarshal(data, &fragment); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if fragment.Kind != "internal" {
			userFacing = true
			break
		}
	}
	if !userFacing {
		return nil
	}
	sourceChangelog, err := readRepositoryFile(root, source, "CHANGELOG.md")
	if err != nil {
		return err
	}
	headChangelog, err := readRepositoryFile(root, head, "CHANGELOG.md")
	if err != nil {
		return err
	}
	if bytes.Equal(sourceChangelog, headChangelog) {
		return errors.New("user-facing changelog fragments were removed without updating CHANGELOG.md")
	}
	receipt, err := fragmentReceipt(root, source)
	if err != nil {
		return err
	}
	if bytes.Contains(sourceChangelog, []byte(receipt)) {
		return errors.New("source CHANGELOG.md already contains its pending-fragment receipt")
	}
	if !bytes.Contains(headChangelog, []byte(receipt)) {
		return errors.New("updated CHANGELOG.md does not contain the frozen fragment receipt")
	}
	return nil
}

func requireReleaseOnlyChanges(root, source, head, manifestPath string) error {
	out, err := gitOutput(root, "diff", "--name-status", source+".."+head)
	if err != nil {
		return err
	}
	allowed := map[string]bool{
		manifestPath:                    true,
		"CHANGELOG.md":                  true,
		"app/package.json":              true,
		"app/pnpm-lock.yaml":            true,
		"app/src-tauri/tauri.conf.json": true,
		"app/src-tauri/Cargo.toml":      true,
		"app/src-tauri/Cargo.lock":      true,
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			return fmt.Errorf("cannot parse candidate change %q", line)
		}
		status, path := fields[0], fields[len(fields)-1]
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			return fmt.Errorf("candidate renames or copies release file %s (%s)", path, status)
		}
		if allowed[path] {
			continue
		}
		if strings.HasPrefix(path, "changelog.d/") && strings.HasSuffix(path, ".yaml") && status == "D" {
			continue
		}
		return fmt.Errorf("candidate changes non-release file %s (%s)", path, status)
	}
	return nil
}

func runAcceptedMain(root string, args []string, stdout io.Writer) error {
	if len(args) == 0 || (args[0] != "tag" && args[0] != "validate") {
		return errors.New("usage: release-train accepted-main <tag|validate> --head <ref> [--manifest path]")
	}
	flags := flag.NewFlagSet("accepted-main "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	headRef := flags.String("head", "", "accepted main ref")
	manifestPath := flags.String("manifest", defaultManifestPath, "candidate manifest")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *headRef == "" {
		return errors.New("head ref is required")
	}
	manifest, err := readAcceptedMainManifest(root, *headRef, *manifestPath)
	if args[0] == "validate" && err == nil {
		manifest, err = validateAcceptedMain(root, *headRef, *manifestPath)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "v"+manifest.Version)
	return err
}

func readAcceptedMainManifest(root, headRef, manifestPath string) (candidateManifest, error) {
	headSHA, err := resolveCommit(root, headRef)
	if err != nil {
		return candidateManifest{}, fmt.Errorf("head: %w", err)
	}
	manifest, err := readManifestAtRef(root, headSHA, manifestPath)
	if err != nil {
		return candidateManifest{}, fmt.Errorf("manifest: %w", err)
	}
	return manifest, nil
}

func validateAcceptedMain(root, headRef, manifestPath string) (candidateManifest, error) {
	headSHA, err := resolveCommit(root, headRef)
	if err != nil {
		return candidateManifest{}, fmt.Errorf("head: %w", err)
	}
	manifest, err := readAcceptedMainManifest(root, headSHA, manifestPath)
	if err != nil {
		return candidateManifest{}, err
	}
	if err := requireReleaseCommit(root, headSHA, manifestPath); err != nil {
		return candidateManifest{}, err
	}
	if err := requireAncestor(root, manifest.MainSHA, headSHA, "recorded main is not an ancestor of accepted main"); err != nil {
		return candidateManifest{}, err
	}
	if err := checkVersions(root, headSHA, manifest.Version); err != nil {
		return candidateManifest{}, err
	}
	if err := requireReleasedFragmentsConsumed(root, manifest.MainSHA, headSHA); err != nil {
		return candidateManifest{}, err
	}
	if err := requireCompiledChangelog(root, manifest.MainSHA, headSHA); err != nil {
		return candidateManifest{}, err
	}
	return manifest, nil
}

func requireReleaseCommit(root, headSHA, manifestPath string) error {
	out, err := gitOutput(root, "log", "-1", "--first-parent", "--format=%H", headSHA, "--", manifestPath)
	if err != nil {
		return err
	}
	releaseSHA := strings.TrimSpace(string(out))
	if releaseSHA != headSHA {
		return fmt.Errorf("%s is not a release commit; %s last changed at %s", headSHA, manifestPath, releaseSHA)
	}
	return nil
}

func fragmentBlobs(root, ref string) (map[string]string, error) {
	out, err := gitOutput(root, "ls-tree", "-r", ref, "--", "changelog.d")
	if err != nil {
		return nil, err
	}
	fragments := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		metadata := strings.Fields(parts[0])
		if len(parts) != 2 || len(metadata) != 3 {
			return nil, fmt.Errorf("cannot parse git tree line %q", line)
		}
		if strings.HasSuffix(parts[1], ".yaml") {
			fragments[parts[1]] = metadata[2]
		}
	}
	return fragments, nil
}

func fragmentReceipt(root, ref string) (string, error) {
	fragments, err := fragmentBlobs(root, ref)
	if err != nil {
		return "", err
	}
	paths := make([]string, 0, len(fragments))
	for path := range fragments {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	digest := sha256.New()
	for _, path := range paths {
		fmt.Fprintf(digest, "%s\x00%s\n", path, fragments[path])
	}
	return fmt.Sprintf("<!-- changelog-fragments-sha256: %x -->", digest.Sum(nil)), nil
}

func normalizeVersion(value string) (string, string, error) {
	plain := strings.TrimPrefix(value, "v")
	if !plainVersionPattern.MatchString(plain) {
		return "", "", fmt.Errorf("version must look like v1.2.3 (got %q)", value)
	}
	return plain, "v" + plain, nil
}

func resolveCommit(root, ref string) (string, error) {
	if ref == "" {
		return "", errors.New("ref is required")
	}
	out, err := gitOutput(root, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func requireAncestor(root, older, newer, message string) error {
	command := exec.Command("git", "merge-base", "--is-ancestor", older, newer)
	command.Dir = root
	err := command.Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return errors.New(message)
	}
	return err
}

func gitOutput(root string, args ...string) ([]byte, error) {
	command := exec.Command("git", args...)
	command.Dir = root
	out, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), message)
	}
	return out, nil
}
