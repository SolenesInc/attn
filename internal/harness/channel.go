package harness

import "context"

// Channel is the optional part of a harness that has its own delivery channel into a session, beside its terminal.
type Channel interface {
	Reach(ctx context.Context, s SessionFacts) Reach
}

type Reach struct{ Up bool }
