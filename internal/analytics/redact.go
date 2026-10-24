package analytics

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const hashedSubjectHexLen = 12

type Allowlist map[string]bool

type redactedString struct {
	S int `json:"s"`
}

func Redact(payload json.RawMessage, allow Allowlist) json.RawMessage {
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil
	}
	redacted, err := json.Marshal(redactValue(value, "", allow))
	if err != nil {
		return nil
	}
	return redacted
}

func redactValue(value any, path string, allow Allowlist) any {
	switch v := value.(type) {
	case string:
		if allow[path] {
			return v
		}
		return redactedString{S: len(v)}
	case map[string]any:
		for key, field := range v {
			v[key] = redactValue(field, joinPath(path, key), allow)
		}
		return v
	case []any:
		for i, item := range v {
			v[i] = redactValue(item, path, allow)
		}
		return v
	default:
		return v
	}
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

func HashSubject(secret []byte, subject string) string {
	if subject == "" {
		return ""
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(subject))
	return "h:" + hex.EncodeToString(mac.Sum(nil))[:hashedSubjectHexLen]
}
