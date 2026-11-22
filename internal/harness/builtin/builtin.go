// Package builtin holds the harnesses built into attn.
package builtin

import (
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/appserver"
	"github.com/victorarias/attn/internal/harness/internal/sigma"
)

// All are the built-in harnesses, their servers bound on servers.
func All(host harness.Host, servers *appserver.Client) []harness.Harness {
	return []harness.Harness{sigma.New(host, servers)}
}
