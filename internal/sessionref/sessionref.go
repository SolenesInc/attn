package sessionref

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/shortid"
)

const (
	Prefix    = "ag-"
	digitBits = 5
	keyHex    = 8
	uuidShape = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
)

type Key uint32

const spareHexBits = 4*keyHex - digitBits*shortid.BodyLen

func KeyOf(ref string) (Key, bool) {
	if body, ok := strings.CutPrefix(ref, Prefix); ok {
		return decode(body)
	}
	if len(ref) < keyHex || !startsUUID(ref) {
		return 0, false
	}
	bits, err := strconv.ParseUint(ref[:keyHex], 16, 32)
	return Key(bits >> spareHexBits), err == nil
}

func Short(id protocol.SessionID) string {
	if len(id) != len(uuidShape) {
		return string(id)
	}
	key, ok := KeyOf(string(id))
	if !ok {
		return string(id)
	}
	return Prefix + encode(key)
}

func Names(ref string, id protocol.SessionID) bool {
	return strings.HasPrefix(string(id), ref) || Short(id) == ref
}

func IDPrefix(ref string) string {
	if !strings.HasPrefix(ref, Prefix) {
		return ref
	}
	key, ok := KeyOf(ref)
	if !ok {
		return ref
	}
	return fmt.Sprintf("%0*x", keyHex, uint32(key)<<spareHexBits)[:keyHex-1]
}

func startsUUID(ref string) bool {
	if len(ref) > len(uuidShape) {
		return false
	}
	for i := range len(ref) {
		if uuidShape[i] == '-' {
			if ref[i] != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdef", rune(ref[i])) {
			return false
		}
	}
	return true
}

func encode(key Key) string {
	out := make([]byte, shortid.BodyLen)
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = shortid.Alphabet[key%Key(len(shortid.Alphabet))]
		key >>= digitBits
	}
	return string(out)
}

func decode(body string) (Key, bool) {
	if len(body) != shortid.BodyLen {
		return 0, false
	}
	var key Key
	for i := range len(body) {
		digit := strings.IndexByte(shortid.Alphabet, body[i])
		if digit < 0 {
			return 0, false
		}
		key = key<<digitBits | Key(digit)
	}
	return key, true
}
