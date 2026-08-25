package workflowresult

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const mcpProtocolVersion = "2024-11-05"

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

// An empty schema means a permissive {"type":"object"}.
func ServeResultSink(
	ctx context.Context,
	toolName string,
	schema json.RawMessage,
	resultPath string,
	input io.Reader,
	output io.Writer,
) error {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		toolName = "return_result"
	}

	schemaObject := map[string]any{"type": "object"}
	if len(schema) > 0 {
		var parsed map[string]any
		if err := json.Unmarshal(schema, &parsed); err != nil {
			return fmt.Errorf("parse result schema: %w", err)
		}
		schemaObject = parsed
	}

	compiled, compileErr := compileSchema(schemaObject)

	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	encoder := json.NewEncoder(output)
	initialized := false

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var request rpcRequest
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			if encodeErr := encoder.Encode(rpcResponse{
				JSONRPC: "2.0",
				Error:   &rpcError{Code: -32700, Message: "failed to parse request"},
			}); encodeErr != nil {
				return encodeErr
			}
			continue
		}
		if request.ID == nil {
			continue
		}

		response := rpcResponse{JSONRPC: "2.0", ID: request.ID}
		switch request.Method {
		case "initialize":
			if initialized {
				response.Error = &rpcError{Code: -32600, Message: "server is already initialized"}
				break
			}
			initialized = true
			response.Result = map[string]any{
				"protocolVersion": mcpProtocolVersion,
				"capabilities": map[string]any{
					"tools": map[string]any{"listChanged": false},
				},
				"serverInfo": map[string]any{
					"name":    "attn-workflow-result",
					"version": "1",
				},
			}
		case "ping":
			response.Result = map[string]any{}
		case "tools/list":
			if !initialized {
				response.Error = &rpcError{Code: -32002, Message: "server is not initialized"}
				break
			}
			response.Result = map[string]any{
				"tools": []map[string]any{
					{
						"name":        toolName,
						"description": "Return the final structured result. Call this exactly once with a JSON object that satisfies the provided schema; the run completes when it is called with a valid payload.",
						"inputSchema": schemaObject,
					},
				},
			}
		case "tools/call":
			if !initialized {
				response.Error = &rpcError{Code: -32002, Message: "server is not initialized"}
				break
			}
			response.Result = callTool(request.Params, toolName, compiled, compileErr, resultPath)
		default:
			response.Error = &rpcError{Code: -32601, Message: "method not found: " + request.Method}
		}
		if err := encoder.Encode(response); err != nil {
			return fmt.Errorf("write MCP response: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read MCP request: %w", err)
	}
	return nil
}

func callTool(
	params map[string]any,
	toolName string,
	compiled *jsonschema.Schema,
	compileErr error,
	resultPath string,
) map[string]any {
	name, _ := params["name"].(string)
	if name != toolName {
		return toolResult("tool not found: "+name, true)
	}
	if compileErr != nil {
		return toolResult("Result schema is invalid: "+compileErr.Error(), true)
	}

	arguments := params["arguments"]
	if arguments == nil {
		arguments = map[string]any{}
	}

	// Number-preserving unmarshal, so validation sees json.Number, not float64.
	rawArgs, err := json.Marshal(arguments)
	if err != nil {
		return toolResult("Validation failed: arguments are not valid JSON: "+err.Error(), true)
	}
	instance, err := jsonschema.UnmarshalJSON(strings.NewReader(string(rawArgs)))
	if err != nil {
		return toolResult("Validation failed: arguments are not valid JSON: "+err.Error(), true)
	}

	if err := compiled.Validate(instance); err != nil {
		return toolResult("Validation failed: "+err.Error(), true)
	}

	if err := writeResult(resultPath, rawArgs); err != nil {
		return toolResult("Failed to record result: "+err.Error(), true)
	}
	return toolResult("Result recorded.", false)
}

func compileSchema(schemaObject map[string]any) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	const loc = "mem://result-schema"
	if err := compiler.AddResource(loc, schemaObject); err != nil {
		return nil, err
	}
	return compiler.Compile(loc)
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

func writeResult(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create result directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".result-*.tmp")
	if err != nil {
		return fmt.Errorf("create result: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("protect result: %w", err)
	}
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return fmt.Errorf("write result: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close result: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace result: %w", err)
	}
	return nil
}
