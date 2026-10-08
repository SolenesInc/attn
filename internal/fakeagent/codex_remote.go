package fakeagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/codexshared"
)

type codexRemote struct {
	cfg          config
	term         *terminal
	path         string
	cwd          string
	model        string
	prompt       string
	mu           sync.Mutex
	client       *codexshared.Client
	connected    chan struct{}
	conversation string
	resumed      bool
	unavailable  bool
	approvals    map[string]json.RawMessage
	answering    map[string]bool
	displayed    string
}

type codexRemoteThread struct {
	Thread struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	} `json:"thread"`
}

var codexReconnectBackoff = []time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond, 160 * time.Millisecond}

func (c *codexRemote) begin(term *terminal) error {
	c.term = term
	c.approvals = map[string]json.RawMessage{}
	c.answering = map[string]bool{}
	c.connected = make(chan struct{})
	args := codexFlags.parse(os.Args[1:])
	c.path = strings.TrimPrefix(args.value("--remote"), "unix://")
	if c.cwd = args.value("-C", "--cd"); c.cwd == "" {
		var err error
		if c.cwd, err = os.Getwd(); err != nil {
			return err
		}
	}
	c.model = args.value("--model", "-m")
	c.prompt = strings.Join(args.afterDashes, " ")
	client, err := c.connect()
	if err != nil {
		return err
	}
	var shown codexRemoteThread
	if len(args.positionals) > 1 && args.positionals[0] == "resume" {
		c.resumed = true
		err = call(client, "thread/resume", map[string]any{"threadId": args.positionals[1], "excludeTurns": true}, &shown)
	} else {
		err = call(client, "thread/start", map[string]any{"cwd": c.cwd, "model": c.model, "ephemeral": false, "threadSource": "user", "config": map[string]any{}}, &shown)
	}
	if err != nil {
		client.Close()
		return err
	}
	c.mu.Lock()
	c.conversation = shown.Thread.ID
	c.mu.Unlock()
	c.attach(client)
	term.title(c.restingTitle())
	c.showPending()
	go c.stayConnected(client)
	return nil
}

func (c *codexRemote) showPending() {
	c.mu.Lock()
	id, pending := c.approvals[c.conversation]
	answering := c.answering[string(id)]
	c.mu.Unlock()
	if pending && !answering {
		c.showApproval(id)
	}
}

func (c *codexRemote) connect() (*codexshared.Client, error) {
	client, err := codexshared.Connect(context.Background(), c.path, "codex-tui", c.observe)
	if err != nil {
		return nil, err
	}
	if err := call(client, methodFakeTerminal, map[string]any{}, nil); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

func call(client *codexshared.Client, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), HangGuard)
	defer cancel()
	raw, err := client.Call(ctx, method, params)
	if err != nil || result == nil {
		return err
	}
	return json.Unmarshal(raw, result)
}

func (c *codexRemote) attach(client *codexshared.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.client = client
	close(c.connected)
}

func (c *codexRemote) stayConnected(client *codexshared.Client) {
	for {
		<-client.Done()
		c.lost(client)
		for attempt := 0; ; attempt++ {
			time.Sleep(codexReconnectBackoff[min(attempt, len(codexReconnectBackoff)-1)])
			next, err := c.connect()
			if err != nil {
				continue
			}
			c.mu.Lock()
			shown := c.conversation
			c.mu.Unlock()
			if err := call(next, "thread/resume", map[string]any{"threadId": shown, "excludeTurns": true}, nil); err != nil {
				c.mu.Lock()
				c.unavailable = true
				c.mu.Unlock()
				c.term.print("This conversation is unavailable. " + err.Error())
			}
			client = next
			c.attach(client)
			break
		}
	}
}

func (c *codexRemote) request(method string, params, result any) error {
	for {
		client, err := c.live()
		if err != nil {
			return err
		}
		err = call(client, method, params, result)
		var refused *codexshared.RPCError
		if err == nil || errors.As(err, &refused) || client.Connected() {
			return err
		}
		<-client.Done()
		c.lost(client)
	}
}

func (c *codexRemote) lost(client *codexshared.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == client {
		c.client, c.connected = nil, make(chan struct{})
	}
}

func (c *codexRemote) live() (*codexshared.Client, error) {
	c.mu.Lock()
	connected := c.connected
	c.mu.Unlock()
	select {
	case <-connected:
	case <-time.After(HangGuard):
		return nil, errors.New("codex --remote stayed disconnected from its app-server")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.unavailable {
		return nil, errors.New("this conversation is unavailable")
	}
	return c.client, nil
}

func (c *codexRemote) observe(m codexshared.Message) {
	var p struct {
		ThreadID string `json:"threadId"`
		Item     struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
	}
	_ = json.Unmarshal(m.Params, &p)
	c.mu.Lock()
	shown := p.ThreadID == c.conversation
	c.mu.Unlock()
	switch {
	case m.Method == codexApprovalRequest:
		c.mu.Lock()
		c.approvals[p.ThreadID] = m.ID
		answering := c.answering[string(m.ID)]
		c.mu.Unlock()
		if shown && !answering {
			c.showApproval(m.ID)
		}
	case m.Method == "serverRequest/resolved":
		c.mu.Lock()
		id, pending := c.approvals[p.ThreadID]
		delete(c.approvals, p.ThreadID)
		answered := c.answering[string(id)]
		delete(c.answering, string(id))
		c.mu.Unlock()
		if pending && shown && !answered {
			_, _ = c.term.closeModal()
		}
	case m.Method == "item/completed" && shown && p.Item.Type == "agentMessage":
		c.term.print(p.Item.Text)
		c.term.title(c.restingTitle())
	}
}

func (c *codexRemote) showApproval(id json.RawMessage) {
	c.mu.Lock()
	again := c.displayed == string(id)
	c.displayed = string(id)
	c.mu.Unlock()
	if again {
		return
	}
	_ = c.term.openModal(&modal{
		title:     c.approvalTitle(),
		lines:     []string{"Allow the command to run?", "› 1. Yes, proceed", "  2. No, and tell Codex what to do differently", "Press enter to confirm or esc to cancel"},
		answering: true,
		resting: func() {
			c.mu.Lock()
			pending := false
			for _, waiting := range c.approvals {
				pending = pending || string(waiting) == string(id)
			}
			c.answering[string(id)] = pending
			c.mu.Unlock()
			c.approvalAnswered()
			if pending {
				go c.answer(id)
			}
		},
	})
}

func (c *codexRemote) answer(id json.RawMessage) {
	if client, err := c.live(); err == nil {
		_ = client.Respond(context.Background(), id, map[string]any{"decision": "accept"})
	}
}

func (c *codexRemote) askApproval() error {
	return c.request(methodFakeApproval, map[string]any{"threadId": c.current()}, nil)
}

func (c *codexRemote) crashServer() error {
	client, err := c.live()
	if err != nil {
		return err
	}
	if err := call(client, methodFakeCrash, map[string]any{}, nil); err != nil {
		return err
	}
	<-client.Done()
	return nil
}

func (c *codexRemote) current() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conversation
}

func (c *codexRemote) restingTitle() string { return "codex" }

func (c *codexRemote) approvalTitle() string { return "[ . ] Action Required | " + c.restingTitle() }

func (c *codexRemote) approvalAnswered() { c.term.title(codexBusyGlyph + c.restingTitle()) }

func (c *codexRemote) launch() launch {
	return launch{Harness: Codex, ConversationID: c.current(), Resumed: c.resumed}
}

func (c *codexRemote) initialPrompt() string { return c.prompt }

func (c *codexRemote) submit(prompt string) error {
	command := strings.TrimSpace(prompt)
	switch {
	case command == "/new" || command == "/clear":
		var started codexRemoteThread
		if err := c.request("thread/start", map[string]any{"cwd": c.cwd, "model": c.model, "ephemeral": false, "threadSource": "user", "config": map[string]any{}}, &started); err != nil {
			return err
		}
		return c.switchTo(started.Thread.ID)
	case strings.HasPrefix(command, "/resume "):
		target := strings.TrimSpace(strings.TrimPrefix(command, "/resume "))
		if err := c.request("thread/resume", map[string]any{"threadId": target, "excludeTurns": true}, nil); err != nil {
			return err
		}
		return c.switchTo(target)
	case strings.HasPrefix(command, "/rename "):
		return c.request("thread/name/set", map[string]any{"threadId": c.current(), "name": strings.TrimSpace(strings.TrimPrefix(command, "/rename "))}, nil)
	}
	c.term.title(codexBusyGlyph + c.restingTitle())
	return c.request("turn/start", map[string]any{"threadId": c.current(), "input": []any{map[string]any{"type": "text", "text": prompt, "text_elements": []any{}}}}, nil)
}

func (c *codexRemote) switchTo(conversation string) error {
	c.mu.Lock()
	previous := c.conversation
	c.conversation = conversation
	c.mu.Unlock()
	c.showPending()
	return c.request("thread/unsubscribe", map[string]any{"threadId": previous}, nil)
}

func (c *codexRemote) reply(text string, afterStop bool) error {
	if afterStop {
		return errors.New("the codex --remote fake does not script a reply written after its Stop hook")
	}
	return c.request(methodFakeReply, map[string]any{"threadId": c.current(), "text": text}, nil)
}

func (c *codexRemote) halt() error {
	return c.request(methodFakeHalt, map[string]any{"threadId": c.current()}, nil)
}
