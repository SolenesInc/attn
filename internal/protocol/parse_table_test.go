package protocol

import (
	"strings"
	"testing"
)

func TestAMalformedMessageNamesItsCommand(t *testing.T) {
	_, _, err := ParseMessage([]byte(`{"cmd":"desktop_create","profile_id":7}`))
	if err == nil || !strings.Contains(err.Error(), "unmarshal desktop_create") {
		t.Fatalf("err = %v, want it to name desktop_create", err)
	}
}
