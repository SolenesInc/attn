package prompts

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"strings"
)

// ManifestVersion changes when the serialized DSL or its rendering semantics change.
const ManifestVersion = 1

const ManifestPath = "catalog.generated.json"

type Manifest struct {
	Version         int         `json:"version"`
	Recipients      []Recipient `json:"recipients"`
	DefinitionsHash string      `json:"definitions_hash,omitempty"`
}

func (c *Catalog) Manifest() Manifest {
	return Manifest{Version: ManifestVersion, Recipients: c.Recipients()}
}

// LoadManifest uses the revision's declarations and sources, never Builtin's declarations.
func LoadManifest(data []byte, sources fs.FS) (*Catalog, error) {
	manifest, err := ParseManifest(data)
	if err != nil {
		return nil, err
	}
	return New(sources, manifest.Recipients...)
}

func ParseManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("invalid catalog manifest: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return manifest, fmt.Errorf("expected one catalog manifest")
	}
	if manifest.Version != ManifestVersion {
		return manifest, fmt.Errorf("unsupported catalog version %d (editor supports %d)", manifest.Version, ManifestVersion)
	}
	return manifest, nil
}

func DefinitionsHash(files fs.FS) (string, error) {
	names, err := fs.Glob(files, "*.go")
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "%s\x00%x\n", name, sha256.Sum256(data))
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
