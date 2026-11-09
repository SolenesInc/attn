package garden

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
)

const CollectionBodyVersions = "body-versions"

type BodyVersion struct {
	Seed    string `json:"seed"`
	Version int    `json:"version"`
	Body    string `json:"body"`
}

func (v BodyVersion) ID() string { return fmt.Sprintf("%s-v%d", v.Seed, v.Version) }

func (v BodyVersion) Encode() ([]byte, error) { return json.Marshal(v) }

func DecodeBodyVersion(body []byte) (BodyVersion, error) {
	var version BodyVersion
	err := json.Unmarshal(body, &version)
	return version, err
}

func BodyVersionsSchema() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace: Namespace, Collection: CollectionBodyVersions,
		Fields: []docstore.FieldSpec{
			{Name: "seed", Type: docstore.FieldString},
			{Name: "version", Type: docstore.FieldNumber},
		},
	}
}

func SeedBodiesSchema() docstore.CollectionSchema {
	return docstore.CollectionSchema{Namespace: Namespace, Collection: CollectionSeedBodies, Text: []string{"body"}}
}

type seedBodyDocument struct {
	Body string `json:"body"`
}

func EncodeSeedBody(body string) ([]byte, error) { return json.Marshal(seedBodyDocument{body}) }

func DecodeSeedBody(data []byte) (string, error) {
	var document seedBodyDocument
	err := json.Unmarshal(data, &document)
	return document.Body, err
}

func BodySHA256(body string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(body))) }

func Stamp(id, body string) string {
	return fmt.Sprintf("<!-- attn-seed %s body-sha256 %s -->\n%s", id, BodySHA256(body), body)
}

func EditRecipe(id string) string {
	return fmt.Sprintf("edit takes an exported file; run attn seed export %s, edit it, then attn seed edit %s -m - < %s.md", id, id, id)
}

func ValidateBodyEdit(body string) error {
	if strings.TrimSpace(body) == "" {
		return protocol.Refuse(protocol.RefusalInvalidInput, "a body edit needs nonempty text; empty or whitespace-only bodies are refused")
	}
	return ValidateBody(body)
}
