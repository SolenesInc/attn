package daemon_test

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorarias/attn/internal/catalog"
	"github.com/victorarias/attn/internal/protocol"
)

// call runs one command as a script's function. The answer's structured content is the value the function returned,
// its text that value as the script would log it, and a refusal's text the code, message and fix the script would catch.
func (c agentToolsCaller) call(t *testing.T, command string, args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()
	return c.callNaming(t, command, args, nil)
}

func (c agentToolsCaller) callNaming(t *testing.T, command string, args map[string]any, meta mcp.Meta) (*mcp.CallToolResult, error) {
	t.Helper()
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.tool(t, "attn_do", map[string]any{"script": fmt.Sprintf(`try { return JSON.stringify({returned: %s(%s)}) } catch (e) {
	if (!(e instanceof Refusal)) throw e;
	return JSON.stringify({refused: e.code + ": " + e.message + (e.fix ? "\nFix: " + e.fix : "")});
}`, command, encoded)}, meta)
	if err != nil || result.IsError {
		return result, err
	}
	var answer struct {
		Returned any
		Refused  string
	}
	if err := json.NewDecoder(strings.NewReader(resultText(result))).Decode(&answer); err != nil {
		t.Fatalf("a script calling %s answered %q: %v", command, resultText(result), err)
	}
	if answer.Refused != "" {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: answer.Refused}}}, nil
	}
	text, isText := answer.Returned.(string)
	if isText {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
	}
	written, err := json.Marshal(answer.Returned)
	if err != nil {
		t.Fatal(err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(written)}}, StructuredContent: answer.Returned}, nil
}

func (c agentToolsCaller) tool(t *testing.T, name string, args map[string]any, meta mcp.Meta) (*mcp.CallToolResult, error) {
	t.Helper()
	session, err := c.connect(t)
	if err != nil {
		return nil, err
	}
	return session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args, Meta: meta})
}

func (c agentToolsCaller) script(t *testing.T, tool, source string) (text string, failed bool) {
	t.Helper()
	result, err := c.tool(t, tool, map[string]any{"script": source}, nil)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return resultText(result), result.IsError
}

func noteScript(seed, text string) map[string]any {
	args, _ := json.Marshal(map[string]any{"seed": seed, "text": text})
	return map[string]any{"script": "seed_note(" + string(args) + ")"}
}

type offeredTools map[string]bool

func listOfferedTools(t *testing.T, agent agentToolsCaller) offeredTools {
	t.Helper()
	names, failed := agent.script(t, "attn_read", `return Object.keys(globalThis).filter(name => typeof globalThis[name] === "function" && name !== "Refusal")`)
	var listed []string
	if err := json.Unmarshal([]byte(names), &listed); failed || err != nil {
		t.Fatalf("a script listing its world's functions answered %q: %v", names, err)
	}
	offered := offeredTools{}
	for _, name := range listed {
		offered[name] = true
	}
	return offered
}

type toolShapes struct {
	t          *testing.T
	agent      agentToolsCaller
	tools      offeredTools
	once       bool
	called     map[string]bool
	mismatches []string
}

func newToolShapes(t *testing.T, agent agentToolsCaller, tools offeredTools) *toolShapes {
	return &toolShapes{t: t, agent: agent, tools: tools, called: map[string]bool{}}
}

func (s *toolShapes) call(tool string, args map[string]any) *mcp.CallToolResult {
	s.t.Helper()
	s.called[tool] = true
	result := s.agent.mustCall(s.t, tool, args)
	if result.IsError {
		s.mismatches = append(s.mismatches, tool+": refused: "+resultText(result))
		return result
	}
	if !s.tools[tool] {
		s.t.Fatalf("%s is not offered", tool)
	}
	cmd := catalogTool(s.t, tool)
	if cmd.Result != "" {
		s.mismatches = append(s.mismatches, protocolModels().differ(ast.NewIdent(cmd.Result), result.StructuredContent, tool)...)
	}
	if cmd.Effect == catalog.EffectRead && !s.once {
		s.compareWithCLI(cmd, args, result)
	}
	return result
}

func (s *toolShapes) object(tool string, args map[string]any) map[string]any {
	s.t.Helper()
	object, _ := s.call(tool, args).StructuredContent.(map[string]any)
	return object
}

func (s *toolShapes) compareWithCLI(cmd *catalog.Command, args map[string]any, result *mcp.CallToolResult) {
	s.t.Helper()
	var caller protocol.PresenceResult
	structuredAs(s.t, s.agent.mustCall(s.t, "presence", map[string]any{}), &caller)
	var printed any
	if err := s.agent.w.Client().RunCommand(cmd.Name, caller.SessionID, args, &printed); err != nil {
		s.mismatches = append(s.mismatches, fmt.Sprintf("attn %s --json: %v", cmd.Name, err))
		return
	}
	returned := result.StructuredContent
	if returned == nil {
		returned = resultText(result)
		if text, _ := printed.(string); toolWords(s.t, text) == returned {
			return
		}
	}
	if !reflect.DeepEqual(printed, returned) {
		s.mismatches = append(s.mismatches, fmt.Sprintf("%s returned %v to a script, and attn %s --json prints %v", cmd.ToolName(), returned, cmd.Name, printed))
	}
}

func (s *toolShapes) finish(prefixes ...string) {
	s.t.Helper()
	var uncalled []string
	for tool := range s.tools {
		if slices.ContainsFunc(prefixes, func(prefix string) bool { return strings.HasPrefix(tool, prefix) }) && !s.called[tool] {
			uncalled = append(uncalled, tool)
		}
	}
	slices.Sort(uncalled)
	if len(uncalled) > 0 {
		s.t.Errorf("offered commands this test never called: %v", uncalled)
	}
	for _, mismatch := range s.mismatches {
		s.t.Error(mismatch)
	}
}

func catalogTool(t *testing.T, tool string) *catalog.Command {
	t.Helper()
	all := builtinCatalog(t)
	for i := range all.Commands {
		if all.Commands[i].IsTool() && all.Commands[i].ToolName() == tool {
			return &all.Commands[i]
		}
	}
	t.Fatalf("the catalog offers no command %s", tool)
	return nil
}

func builtinCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	all, err := catalog.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func toolWords(t *testing.T, reference string) string {
	t.Helper()
	return builtinCatalog(t).TeachingToolText(reference)
}

// models are the structs and named types of the protocol package, which the daemon answers the CLI with.
type models map[string]ast.Expr

var protocolModels = sync.OnceValue(func() models {
	files, err := filepath.Glob(filepath.Join("..", "protocol", "*.go"))
	if err != nil {
		panic(err)
	}
	found := models{}
	for _, file := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			panic(err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			if spec, ok := node.(*ast.TypeSpec); ok {
				found[spec.Name.Name] = spec.Type
			}
			return true
		})
	}
	return found
})

func (m models) differ(expr ast.Expr, value any, at string) []string {
	switch kind := expr.(type) {
	case *ast.StarExpr:
		return m.differ(kind.X, value, at)
	case *ast.Ident:
		if named, ok := m[kind.Name]; ok {
			return m.differ(named, value, at)
		}
		want := map[string]string{"string": "string", "bool": "bool", "int": "float64", "int64": "float64", "float64": "float64"}[kind.Name]
		if want != "" && fmt.Sprintf("%T", value) != want {
			return []string{fmt.Sprintf("%s is %v, want a %s", at, value, kind.Name)}
		}
	case *ast.ArrayType:
		items, isList := value.([]any)
		if !isList {
			return []string{fmt.Sprintf("%s is %v, want a list", at, value)}
		}
		var problems []string
		for i, item := range items {
			problems = append(problems, m.differ(kind.Elt, item, fmt.Sprintf("%s[%d]", at, i))...)
		}
		return problems
	case *ast.StructType:
		object, isObject := value.(map[string]any)
		if !isObject {
			return []string{fmt.Sprintf("%s is %v, want an object", at, value)}
		}
		left := maps.Clone(object)
		var problems []string
		for _, field := range kind.Fields.List {
			if field.Tag == nil {
				continue
			}
			name, options, _ := strings.Cut(reflect.StructTag(strings.Trim(field.Tag.Value, "`")).Get("json"), ",")
			inner, given := left[name]
			delete(left, name)
			if !given && !strings.Contains(options, "omitempty") {
				problems = append(problems, at+" lacks "+name)
			}
			if given && inner != nil {
				problems = append(problems, m.differ(field.Type, inner, at+"."+name)...)
			}
		}
		for name := range left {
			problems = append(problems, at+" holds "+name+", which its model lacks")
		}
		return problems
	}
	return nil
}
