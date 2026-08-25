package pty

// Semantics-identical port of app/src/utils/terminalOsc133.ts. Parity is enforced by the shared
// corpus testdata/osc133_segmenter_corpus.json, consumed here AND by a frontend parity test.

import (
	"net/url"
	"strconv"
	"strings"
)

var osc133Prefix = []byte{0x1b, 0x5d, 0x31, 0x33, 0x33, 0x3b}

const (
	oscBEL       = 0x07
	oscESC       = 0x1b
	oscBackslash = 0x5c
)

func osc133MarkerFromPayload(payload string) *osc133Marker {
	if payload == "" {
		return nil
	}
	switch payload[0] {
	case 'A':
		return &osc133Marker{Kind: osc133PromptStart}
	case 'B':
		return &osc133Marker{Kind: osc133InputStart}
	case 'C':
		var cmdline *string
		rest := ""
		if len(payload) > 2 {
			rest = payload[2:]
		}
		for _, part := range strings.Split(rest, ";") {
			switch {
			case strings.HasPrefix(part, "cmdline_url="):
				// Percent-decode without treating '+' as space: url.PathUnescape, not QueryUnescape.
				if dec, err := url.PathUnescape(part[len("cmdline_url="):]); err == nil {
					c := dec
					cmdline = &c
				} else {
					cmdline = nil
				}
			case strings.HasPrefix(part, "cmdline=") && cmdline == nil:
				c := part[len("cmdline="):]
				cmdline = &c
			}
		}
		return &osc133Marker{Kind: osc133PreExec, Cmdline: cmdline}
	case 'D':
		var exitCode *int32
		rest := ""
		if len(payload) > 2 {
			rest = payload[2:]
		}
		if v, ok := parseInt10Prefix(rest); ok {
			exitCode = &v
		}
		return &osc133Marker{Kind: osc133CommandEnd, ExitCode: exitCode}
	default:
		return nil
	}
}

// Mirrors JS parseInt(s, 10), keeping exit-code parsing byte-for-byte with the client parser.
func parseInt10Prefix(s string) (int32, bool) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '\f' || s[i] == '\v') {
		i++
	}
	start := i
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digitStart := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == digitStart {
		return 0, false
	}
	n, err := strconv.Atoi(s[start:i])
	if err != nil {
		return 0, false
	}
	return int32(n), true
}
