package daemon

import (
	"github.com/victorarias/attn/internal/ptybackend"
)

func spawnTestClient() *wsClient {
	return &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
}
