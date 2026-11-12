package client

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/harness"
)

var serverCallerOnce sync.Once
var serverCaller harness.TerminalID

func TerminalFromEnv() harness.TerminalID {
	if terminal := harness.TerminalFromEnv(); terminal != "" {
		return terminal
	}
	serverCallerOnce.Do(func() {
		c := &http.Client{Timeout: time.Second}
		response, err := c.Get("http://127.0.0.1:" + config.WSPort() + "/agents")
		if err != nil {
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return
		}
		var catalog struct {
			Agents []struct {
				Name                  string `json:"name"`
				CallerConversationEnv string `json:"caller_conversation_env"`
			} `json:"agents"`
		}
		if json.NewDecoder(response.Body).Decode(&catalog) != nil {
			return
		}
		for _, agent := range catalog.Agents {
			if agent.CallerConversationEnv == "" {
				continue
			}
			if conversation := os.Getenv(agent.CallerConversationEnv); conversation != "" {
				serverCaller = harness.TerminalID("server:" + agent.Name + ":" + conversation)
				return
			}
		}
	})
	return serverCaller
}
