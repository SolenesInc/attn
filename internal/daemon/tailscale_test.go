package daemon

import (
	"context"
)

type fakeTailscaleCLI struct {
	calls [][]string
	run   func(args []string) ([]byte, error)
}

func (f *fakeTailscaleCLI) Run(_ context.Context, args ...string) ([]byte, error) {
	call := append([]string(nil), args...)
	f.calls = append(f.calls, call)
	if f.run == nil {
		return nil, nil
	}
	return f.run(call)
}
