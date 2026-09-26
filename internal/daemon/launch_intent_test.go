package daemon

import (
	"context"
	"errors"

	"github.com/victorarias/attn/internal/ptybackend"
)

type failingLaunchIntentBackend struct {
	fakeSpawnBackend
}

func (b *failingLaunchIntentBackend) Spawn(context.Context, ptybackend.SpawnOptions) error {
	return errors.New("spawn failed")
}
