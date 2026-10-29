package garden

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
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

func BodySHA256(body string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(body))) }

func Stamp(id, body string) string {
	return fmt.Sprintf("<!-- attn-seed %s body-sha256 %s -->\n%s", id, BodySHA256(body), body)
}

var bodyStamp = regexp.MustCompile(`^<!-- attn-seed (\S+) body-sha256 ([0-9a-f]{64}) -->$`)

func EditRecipe(id string) string {
	return fmt.Sprintf("edit takes an exported file; run attn seed export %s, edit it, then attn seed edit %s -m - < %s.md", id, id, id)
}

func ValidateBodyEdit(body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("a body edit needs nonempty text; empty or whitespace-only bodies are refused")
	}
	return ValidateBody(body)
}

func ParseStamped(id, text string) (body, base string, err error) {
	line, body, found := strings.Cut(text, "\n")
	stamp := bodyStamp.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
	if !found || stamp == nil {
		return "", "", fmt.Errorf("%s", EditRecipe(id))
	}
	if stamp[1] != id {
		return "", "", fmt.Errorf("this export belongs to %s, not %s; %s", stamp[1], id, EditRecipe(id))
	}
	if err := ValidateBodyEdit(body); err != nil {
		return "", "", err
	}
	return body, stamp[2], nil
}
