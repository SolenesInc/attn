package git

import (
	"context"
	"errors"
	"testing"
)

func TestNewestTreeModTimeContextReturnsCancellationCause(t *testing.T) {
	cause := errors.New("foreground preempted sweep")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	_, err := NewestTreeModTimeContext(ctx, t.TempDir())
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want cancellation cause", err)
	}
}
