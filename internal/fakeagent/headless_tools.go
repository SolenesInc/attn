package fakeagent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type toolServer struct {
	command string
	args    []string
	tools   []string
}

func (task *HeadlessTask) CallTool(arguments string) {
	task.answer <- headlessAnswer{Tool: json.RawMessage(arguments)}
}

func (task *HeadlessTask) CallToolAndFail(arguments, message string) {
	task.answer <- headlessAnswer{Tool: json.RawMessage(arguments), Failure: message}
}

func codexToolServers(overrides []string) (map[string]*toolServer, bool) {
	servers := map[string]*toolServer{}
	for _, override := range overrides {
		rest, ok := strings.CutPrefix(override, "mcp_servers.")
		if !ok {
			continue
		}
		key, value, _ := strings.Cut(rest, "=")
		name, field, _ := strings.Cut(key, ".")
		server := servers[name]
		if server == nil {
			server = &toolServer{}
			servers[name] = server
		}
		switch field {
		case "command":
			server.command, _ = strconv.Unquote(value)
		case "args":
			_ = json.Unmarshal([]byte(value), &server.args)
		case "enabled_tools":
			_ = json.Unmarshal([]byte(value), &server.tools)
		}
	}
	for _, server := range servers {
		if server.command == "" {
			return nil, false
		}
	}
	return servers, true
}

func callTool(servers map[string]*toolServer, arguments json.RawMessage) error {
	var target *toolServer
	for _, server := range servers {
		if len(server.tools) > 0 {
			if target != nil {
				return errors.New("the fake calls a tool only when exactly one server offers one")
			}
			target = server
		}
	}
	if target == nil {
		return errors.New("the test answered with a tool call and no server offers a tool")
	}
	cmd := exec.Command(target.command, target.args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}()
	replies := bufio.NewScanner(stdout)
	replies.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for id, request := range []map[string]any{
		{"method": "initialize", "params": map[string]any{"protocolVersion": "2024-11-05"}},
		{"method": "tools/call", "params": map[string]any{"name": target.tools[0], "arguments": arguments}},
	} {
		request["jsonrpc"], request["id"] = "2.0", id+1
		line, err := json.Marshal(request)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(stdin, "%s\n", line); err != nil {
			return err
		}
		if !replies.Scan() {
			return fmt.Errorf("the tool server closed before answering %s", request["method"])
		}
		var reply struct {
			Error *struct{ Message string } `json:"error"`
		}
		if err := json.Unmarshal(replies.Bytes(), &reply); err != nil {
			return err
		}
		if reply.Error != nil {
			return fmt.Errorf("%s: %s", request["method"], reply.Error.Message)
		}
	}
	return nil
}
