package garden

import (
	"strings"
	"testing"
)

func TestStampedBodiesPreserveBytesAndRequireTheirSeedAndHash(t *testing.T) {
	const id = "s-7k3f9m"
	for _, body := range []string{"a", "a\n", "a\n\n", "a  \r\n\t", "## Findings\n\nà 🐈\n"} {
		got, base, err := ParseStamped(id, Stamp(id, body))
		if err != nil || got != body || base != BodySHA256(body) {
			t.Fatalf("round trip %q = %q, %s, %v", body, got, base, err)
		}
	}
	for _, text := range []string{
		"new body", "", Stamp("s-abcdef", "wrong seed"), Stamp(id, ""), Stamp(id, " \n\t"),
		strings.Replace(Stamp(id, "body"), "body-sha256", "revision", 1),
		strings.Replace(Stamp(id, "body"), BodySHA256("body"), "bad-hash", 1),
	} {
		if _, _, err := ParseStamped(id, text); err == nil {
			t.Fatalf("accepted invalid export %q", text)
		}
	}
}
