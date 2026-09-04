package ptybackend

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadMatchingResponseSkipsUnrelatedFrames(t *testing.T) {
	dec := json.NewDecoder(strings.NewReader(`
		{"type":"evt","event":"state_changed","session_id":"terminal"}
		{"type":"res","id":"other","ok":false,"error":{"code":"io_error","message":"unrelated"}}
		{"type":"res","id":"wanted","ok":true,"result":{"child_pid":42}}
		{"type":"res","id":"next","ok":true}
	`))
	res, err := readMatchingResponse(dec, "wanted")
	if err != nil || !res.OK || string(res.Result) != `{"child_pid":42}` {
		t.Fatalf("response = %+v, error = %v", res, err)
	}
	next, err := readMatchingResponse(dec, "next")
	if err != nil || next.ID != "next" {
		t.Fatalf("consumed the following response: %+v, %v", next, err)
	}
}

func TestReadMatchingResponsePreservesRPCError(t *testing.T) {
	dec := json.NewDecoder(strings.NewReader(`{"type":"res","id":"wanted","ok":false,"error":{"code":"session_not_found","message":"gone"}}`))
	res, err := readMatchingResponse(dec, "wanted")
	if err != nil || res.OK || res.Error == nil || res.Error.Code != "session_not_found" || res.Error.Message != "gone" {
		t.Fatalf("response = %+v, error = %v", res, err)
	}
}

func TestReadMatchingResponseStopsOnInvalidOrEndedStream(t *testing.T) {
	for _, input := range []string{"", `{`, `{"type":"unexpected"}`, `{"type":"res","id":42}`} {
		t.Run(input, func(t *testing.T) {
			_, err := readMatchingResponse(json.NewDecoder(strings.NewReader(input)), "wanted")
			if err == nil {
				t.Fatal("expected a stream error")
			}
			if input == "" && !errors.Is(err, io.EOF) {
				t.Fatalf("error = %v, want EOF", err)
			}
		})
	}
}
