package analytics

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
)

const hashedSubjectHexLen = 12

type Field uint8

const (
	Safe Field = iota + 1
	ID
	Actor
)

var namedActors = map[string]bool{"user": true, "attn": true}

type Fields map[string]Field

type redactedString struct {
	S int `json:"s"`
}

type redactedObject struct {
	N int `json:"n"`
}

var identifierKey = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

func Redact(payload json.RawMessage, fields Fields, secret []byte) json.RawMessage {
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil
	}
	redacted, err := json.Marshal(redactValue(value, "", fields, secret))
	if err != nil {
		return nil
	}
	return redacted
}

func redactValue(value any, path string, fields Fields, secret []byte) any {
	switch v := value.(type) {
	case string:
		switch fields[path] {
		case Safe:
			return v
		case ID:
			return HashSubject(secret, v)
		case Actor:
			if namedActors[v] {
				return v
			}
			return HashSubject(secret, v)
		}
		return redactedString{S: len(v)}
	case map[string]any:
		for key := range v {
			if !identifierKey.MatchString(key) {
				return redactedObject{N: len(v)}
			}
		}
		for key, field := range v {
			v[key] = redactValue(field, joinPath(path, key), fields, secret)
		}
		return v
	case []any:
		for i, item := range v {
			v[i] = redactValue(item, path, fields, secret)
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
