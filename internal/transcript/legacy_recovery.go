package transcript

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	jsast "github.com/dop251/goja/ast"
	jsparser "github.com/dop251/goja/parser"
	"github.com/victorarias/attn/internal/toolhome"
	"mvdan.cc/sh/v3/syntax"
)

const (
	LegacyRecoveryRecordLimit     = 8 << 20
	LegacyRecoveryTranscriptLimit = 512 << 20
)

var (
	legacyTicketReceiptPattern = regexp.MustCompile(`(?m)ticket ([a-z0-9][a-z0-9-]*) → (working|blocked|in_review|done|failed|crashed)`)
	legacyCellIDPattern        = regexp.MustCompile(`(?i)(?:cell ID|cell_id)[[:space:]` + "`" + `"':=]+([A-Za-z0-9_-]+)`)
	legacyCodexAnchorPattern   = regexp.MustCompile(`attn checked out this workspace's shared context for this session at "([^"]+/context\.md)"\.`)
)

var (
	ErrLegacyRecoveryRecordTooLarge     = errors.New("transcript record exceeds recovery safety limit")
	ErrLegacyRecoveryTranscriptTooLarge = errors.New("transcript exceeds recovery safety limit")
)

type LegacyRecoveryRoots struct {
	Codex   string
	Claude  string
	Copilot string
}

type LegacyRecoverySource struct {
	Provider        string
	Path            string
	NativeSessionID string
}

type LegacyTicketReceipt struct {
	TicketID    string
	State       string
	Timestamp   time.Time
	Bound       bool
	Explicit    bool
	Transcript  LegacyRecoverySource
	Fingerprint string
}

type LegacyTranscriptInspection struct {
	Source       LegacyRecoverySource
	Production   bool
	CWD          string
	FirstHuman   string
	Receipts     []LegacyTicketReceipt
	Conversation string
	Warnings     []string
}

func ResolveLegacyRecoveryRoots() (LegacyRecoveryRoots, error) {
	home, err := toolhome.Dir()
	if err != nil {
		return LegacyRecoveryRoots{}, err
	}
	codex := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	return LegacyRecoveryRoots{
		Codex:   filepath.Join(codex, "sessions"),
		Claude:  filepath.Join(home, ".claude", "projects"),
		Copilot: filepath.Join(home, ".copilot", "session-state"),
	}, nil
}

func EnumerateLegacyRecoverySources(roots LegacyRecoveryRoots) ([]LegacyRecoverySource, error) {
	var sources []LegacyRecoverySource
	for _, providerRoot := range []struct {
		provider string
		root     string
	}{
		{provider: "codex", root: roots.Codex},
		{provider: "claude", root: roots.Claude},
	} {
		if strings.TrimSpace(providerRoot.root) == "" {
			continue
		}
		err := filepath.WalkDir(providerRoot.root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
			}
			if path == providerRoot.root {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			native := strings.TrimSuffix(entry.Name(), ".jsonl")
			if providerRoot.provider == "codex" {
				native = readCodexNativeSessionID(path)
				if native == "" {
					return nil
				}
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			sources = append(sources, LegacyRecoverySource{
				Provider: providerRoot.provider, Path: filepath.Clean(abs), NativeSessionID: native,
			})
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("enumerate %s transcripts: %w", providerRoot.provider, err)
		}
	}

	if strings.TrimSpace(roots.Copilot) != "" {
		entries, err := os.ReadDir(roots.Copilot)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("enumerate copilot transcripts: %w", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			path := filepath.Join(roots.Copilot, entry.Name(), "events.jsonl")
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return nil, err
			}
			sources = append(sources, LegacyRecoverySource{
				Provider: "copilot", Path: filepath.Clean(abs), NativeSessionID: entry.Name(),
			})
		}
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].Provider != sources[j].Provider {
			return sources[i].Provider < sources[j].Provider
		}
		return sources[i].Path < sources[j].Path
	})
	return sources, nil
}

func LegacyRecoverySourceAt(provider, path string) LegacyRecoverySource {
	provider = strings.ToLower(strings.TrimSpace(provider))
	native := ""
	switch provider {
	case "codex":
		native = readCodexNativeSessionID(path)
	case "claude":
		native = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	case "copilot":
		native = filepath.Base(filepath.Dir(path))
	}
	return LegacyRecoverySource{Provider: provider, Path: filepath.Clean(path), NativeSessionID: native}
}

func readCodexNativeSessionID(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	record, err := readLegacyRecoveryRecord(bufio.NewReader(f))
	if err != nil {
		return ""
	}
	var line struct {
		Type    string `json:"type"`
		Payload struct {
			ID string `json:"id"`
		} `json:"payload"`
	}
	if json.Unmarshal(record, &line) != nil || line.Type != "session_meta" {
		return ""
	}
	return strings.TrimSpace(line.Payload.ID)
}

func InspectLegacyRecoveryTranscript(source LegacyRecoverySource, dataRoot string) (LegacyTranscriptInspection, error) {
	inspection := LegacyTranscriptInspection{Source: source}
	info, err := os.Lstat(source.Path)
	if err != nil {
		return inspection, err
	}
	// The largest receipt-bearing local transcript measured 251,829,243 bytes.
	// Twice that corpus high-water mark is a tripwire for damaged or hostile input.
	if info.Size() > LegacyRecoveryTranscriptLimit {
		return inspection, fmt.Errorf("%w (%d bytes)", ErrLegacyRecoveryTranscriptTooLarge, info.Size())
	}
	hasReceipt, err := fileContainsLegacyReceiptMarker(source.Path)
	if err != nil || !hasReceipt {
		return inspection, err
	}

	records, err := readLegacyRecoveryRecords(source.Path)
	if err != nil {
		return inspection, err
	}

	switch source.Provider {
	case "codex":
		inspection.Production, inspection.CWD = proveCodexProduction(records, dataRoot, source.NativeSessionID)
	case "claude":
		inspection.Production, inspection.CWD = proveClaudeProduction(records, dataRoot, source.NativeSessionID)
	case "copilot":
		inspection.Production, inspection.CWD = proveCopilotProduction(records, source.NativeSessionID)
	default:
		return inspection, fmt.Errorf("unsupported transcript provider %q", source.Provider)
	}
	inspection.Receipts = proveLegacyTicketReceipts(source, records)
	if !inspection.Production {
		return inspection, nil
	}
	for _, receipt := range inspection.Receipts {
		if !receipt.Bound {
			continue
		}
		inspection.Conversation, inspection.FirstHuman = renderLegacyConversation(source, records)
		break
	}
	return inspection, nil
}

func proveCopilotProduction(records [][]byte, native string) (bool, string) {
	native = strings.TrimSpace(native)
	seenHuman := false
	seenStart := false
	cwd := ""
	for _, record := range records {
		var envelope struct {
			Type string `json:"type"`
			Data struct {
				SessionID string `json:"sessionId"`
				Producer  string `json:"producer"`
				StartTime string `json:"startTime"`
				Context   struct {
					CWD string `json:"cwd"`
				} `json:"context"`
			} `json:"data"`
		}
		if json.Unmarshal(record, &envelope) != nil {
			continue
		}
		if envelope.Type == "user.message" {
			seenHuman = true
			continue
		}
		if envelope.Type != "session.start" {
			continue
		}
		startTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(envelope.Data.StartTime))
		startCWD := filepath.Clean(strings.TrimSpace(envelope.Data.Context.CWD))
		if seenHuman || seenStart || native == "" || strings.TrimSpace(envelope.Data.SessionID) != native ||
			strings.TrimSpace(envelope.Data.Producer) != "copilot-agent" || err != nil || startTime.IsZero() ||
			startCWD == "." || !filepath.IsAbs(startCWD) {
			return false, cwd
		}
		seenStart = true
		cwd = startCWD
	}
	// Copilot did not persist profile routing. The one-time default-profile migration
	// accepts its native envelope because Copilot was not used under named profiles.
	return seenStart, cwd
}

func fileContainsLegacyReceiptMarker(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	chunk := make([]byte, 64<<10)
	var tail []byte
	for {
		n, readErr := f.Read(chunk)
		window := append(tail, chunk[:n]...)
		if bytes.Contains(window, []byte("ticket ")) &&
			(bytes.Contains(window, []byte("→")) || bytes.Contains(window, []byte(`\u2192`))) {
			return true, nil
		}
		const overlap = 32
		if len(window) > overlap {
			tail = append(tail[:0], window[len(window)-overlap:]...)
		} else {
			tail = append(tail[:0], window...)
		}
		if errors.Is(readErr, io.EOF) {
			return false, nil
		}
		if readErr != nil {
			return false, readErr
		}
	}
}

func readLegacyRecoveryRecords(path string) ([][]byte, error) {
	return readLegacyRecoveryRecordsWithLimit(path, LegacyRecoveryTranscriptLimit)
}

func readLegacyRecoveryRecordsWithLimit(path string, limit int64) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil {
		return nil, err
	} else if info.Size() > limit {
		return nil, fmt.Errorf("%w (%d bytes)", ErrLegacyRecoveryTranscriptTooLarge, info.Size())
	}
	reader := bufio.NewReaderSize(f, 64<<10)
	var records [][]byte
	var total int64
	for {
		record, err := readLegacyRecoveryRecord(reader)
		if len(record) > 0 {
			total += int64(len(record)) + 1
			if total > limit {
				return nil, fmt.Errorf("%w (%d bytes)", ErrLegacyRecoveryTranscriptTooLarge, total)
			}
			records = append(records, record)
		}
		if errors.Is(err, io.EOF) {
			return records, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func readLegacyRecoveryRecord(reader *bufio.Reader) ([]byte, error) {
	var record []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(record)+len(fragment) > LegacyRecoveryRecordLimit {
			return nil, fmt.Errorf("%w (%d bytes)", ErrLegacyRecoveryRecordTooLarge, len(record)+len(fragment))
		}
		record = append(record, fragment...)
		if err == nil {
			return bytes.TrimSpace(record), nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return bytes.TrimSpace(record), err
		}
	}
}

func proveCodexProduction(records [][]byte, dataRoot, native string) (bool, string) {
	contextRoot := filepath.Clean(filepath.Join(dataRoot, "workspace-contexts")) + string(os.PathSeparator)
	cwd := ""
	seenHuman := false
	seenMeta := false
	for _, record := range records {
		var envelope struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string          `json:"type"`
				ID      string          `json:"id"`
				CWD     string          `json:"cwd"`
				Role    string          `json:"role"`
				Message string          `json:"message"`
				Content json.RawMessage `json:"content"`
			} `json:"payload"`
		}
		if json.Unmarshal(record, &envelope) != nil {
			continue
		}
		if envelope.Type == "session_meta" {
			seenMeta = strings.TrimSpace(envelope.Payload.ID) == strings.TrimSpace(native)
			cwd = filepath.Clean(envelope.Payload.CWD)
			continue
		}
		if envelope.Type == "event_msg" && envelope.Payload.Type == "user_message" {
			seenHuman = true
		}
		if seenHuman || envelope.Type != "response_item" || envelope.Payload.Type != "message" || envelope.Payload.Role != "developer" {
			continue
		}
		text := extractTextContent(envelope.Payload.Content)
		matches := legacyCodexAnchorPattern.FindAllStringSubmatch(text, -1)
		if len(matches) != 1 {
			continue
		}
		path := filepath.Clean(matches[0][1])
		if strings.HasPrefix(path, contextRoot) && filepath.Base(path) == "context.md" && seenMeta {
			return true, cwd
		}
	}
	return false, cwd
}

type legacyCheckoutMetadata struct {
	WorkspaceID   string `json:"workspace_id"`
	SessionID     string `json:"session_id"`
	CanonicalHash string `json:"canonical_hash"`
	CheckedOutAt  string `json:"checked_out_at"`
}

func proveClaudeProduction(records [][]byte, dataRoot, native string) (bool, string) {
	sum := sha256.Sum256([]byte(native))
	dir := filepath.Join(dataRoot, "workspace-contexts", hex.EncodeToString(sum[:8]))
	contextPath := filepath.Join(dir, "context.md")
	metadataPath := filepath.Join(dir, "checkout.json")
	contextInfo, contextErr := os.Lstat(contextPath)
	metadataInfo, metadataErr := os.Lstat(metadataPath)
	if contextErr != nil || metadataErr != nil || !contextInfo.Mode().IsRegular() || !metadataInfo.Mode().IsRegular() {
		return false, ""
	}
	metadataBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		return false, ""
	}
	var metadata legacyCheckoutMetadata
	if json.Unmarshal(metadataBytes, &metadata) != nil || metadata.SessionID != native || metadata.WorkspaceID == "" || metadata.CanonicalHash == "" {
		return false, ""
	}
	contextBytes, err := os.ReadFile(contextPath)
	if err != nil {
		return false, ""
	}
	contextHash := sha256.Sum256(contextBytes)
	if hex.EncodeToString(contextHash[:]) != metadata.CanonicalHash {
		return false, ""
	}
	checkedOutAt, err := time.Parse(time.RFC3339Nano, metadata.CheckedOutAt)
	if err != nil {
		return false, ""
	}

	toolOrdinal := 0
	earlyRead := false
	nearCheckout := false
	cwd := ""
	for _, record := range records {
		var envelope struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			CWD       string `json:"cwd"`
			SessionID string `json:"sessionId"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(record, &envelope) != nil {
			continue
		}
		if envelope.SessionID != "" && envelope.SessionID != native {
			return false, cwd
		}
		if envelope.CWD != "" {
			clean := filepath.Clean(envelope.CWD)
			if cwd == "" {
				cwd = clean
			} else if cwd != clean {
				cwd = ""
			}
		}
		if ts, parseErr := time.Parse(time.RFC3339Nano, envelope.Timestamp); parseErr == nil && absDuration(ts.Sub(checkedOutAt)) <= 10*time.Minute {
			nearCheckout = true
		}
		if envelope.Type != "assistant" {
			continue
		}
		for _, block := range decodeEventContent(envelope.Message.Content) {
			if block.Type != "tool_use" {
				continue
			}
			toolOrdinal++
			if toolOrdinal > 3 || block.Name != "Read" {
				continue
			}
			var input struct {
				FilePath string `json:"file_path"`
			}
			if json.Unmarshal(block.Input, &input) == nil && filepath.Clean(input.FilePath) == contextPath {
				earlyRead = true
			}
		}
	}
	return earlyRead && nearCheckout, cwd
}

type legacyToolCall struct {
	name      string
	input     string
	timestamp time.Time
}

type legacyToolResult struct {
	callID    string
	text      string
	timestamp time.Time
	isError   bool
}

func proveLegacyTicketReceipts(source LegacyRecoverySource, records [][]byte) []LegacyTicketReceipt {
	calls := make(map[string]legacyToolCall)
	var results []legacyToolResult
	for _, record := range records {
		var envelope eventEnvelope
		if json.Unmarshal(record, &envelope) != nil {
			continue
		}
		for _, event := range parseEventLine(source.Provider, record).events {
			ts, _ := time.Parse(time.RFC3339Nano, event.Timestamp)
			switch event.Kind {
			case EventKindToolCall:
				calls[event.ToolCallID] = legacyToolCall{name: event.ToolName, input: event.Text, timestamp: ts}
			case EventKindToolResult:
				results = append(results, legacyToolResult{callID: event.ToolCallID, text: event.Text, timestamp: ts, isError: event.IsError})
			}
		}
	}

	cells := make(map[string][]legacyStatusCall)
	for _, result := range results {
		if result.isError {
			continue
		}
		call, ok := calls[result.callID]
		if !ok {
			continue
		}
		statusCalls := legacyStatusCalls(call.name, call.input)
		if cell := legacyCellID(result.text); len(statusCalls) > 0 && cell != "" && len(legacyReceipts(result.text)) == 0 {
			cells[cell] = statusCalls
		}
	}

	var out []LegacyTicketReceipt
	for _, result := range results {
		if result.isError {
			continue
		}
		call, ok := calls[result.callID]
		if !ok {
			continue
		}
		statusCalls := legacyStatusCalls(call.name, call.input)
		if len(statusCalls) == 0 {
			if cell := legacyContinuationCell(call.name, call.input); cell != "" {
				statusCalls = cells[cell]
			}
		}
		if len(statusCalls) == 0 {
			continue
		}
		receipts := legacyReceipts(result.text)
		for _, receipt := range receipts {
			matched := false
			explicit := false
			for _, statusCall := range statusCalls {
				if statusCall.state == receipt.state {
					matched = true
					explicit = explicit || statusCall.explicit
				}
			}
			if !matched {
				continue
			}
			bound := len(receipts) == 1 && len(statusCalls) == 1 && statusCalls[0].simple && !statusCalls[0].explicit
			ts := result.timestamp
			if ts.IsZero() {
				ts = call.timestamp
			}
			fingerprintBytes := sha256.Sum256([]byte(strings.Join([]string{
				source.Provider, source.NativeSessionID, result.callID, receipt.ticketID, receipt.state, ts.UTC().Format(time.RFC3339Nano),
			}, "\x00")))
			out = append(out, LegacyTicketReceipt{
				TicketID: receipt.ticketID, State: receipt.state, Timestamp: ts, Bound: bound,
				Explicit: explicit, Transcript: source, Fingerprint: hex.EncodeToString(fingerprintBytes[:]),
			})
		}
	}
	return out
}

type legacyReceipt struct {
	ticketID string
	state    string
}

func legacyReceipts(text string) []legacyReceipt {
	matches := legacyTicketReceiptPattern.FindAllStringSubmatch(text, -1)
	out := make([]legacyReceipt, 0, len(matches))
	seen := make(map[legacyReceipt]struct{}, len(matches))
	for _, match := range matches {
		receipt := legacyReceipt{ticketID: match[1], state: match[2]}
		if _, ok := seen[receipt]; ok {
			continue
		}
		seen[receipt] = struct{}{}
		out = append(out, receipt)
	}
	return out
}

func legacyCellID(text string) string {
	match := legacyCellIDPattern.FindStringSubmatch(text)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

type legacyStatusCall struct {
	state    string
	explicit bool
	simple   bool
}

func legacyStatusCalls(toolName, input string) []legacyStatusCall {
	var commands []string
	if command := legacyShellCommandFromJSON(input); command != "" {
		commands = append(commands, command)
	}
	if strings.Contains(strings.ToLower(toolName), "exec") || strings.Contains(input, "tools.exec_command") {
		commands = append(commands, legacyJSStringProperties(input, "exec_command", "cmd", "command")...)
	}
	if len(commands) == 0 && legacyShellTool(toolName) {
		commands = append(commands, input)
	}
	var out []legacyStatusCall
	for _, command := range commands {
		out = append(out, parseLegacyShellStatusCalls(command)...)
	}
	return out
}

func legacyContinuationCell(toolName, input string) string {
	if strings.Contains(strings.ToLower(toolName), "wait") || strings.Contains(input, "tools.wait") {
		values := legacyJSStringProperties(input, "wait", "cell_id")
		if len(values) == 1 {
			return values[0]
		}
		var value struct {
			CellID string `json:"cell_id"`
		}
		if json.Unmarshal([]byte(input), &value) == nil {
			return value.CellID
		}
	}
	return ""
}

func legacyShellTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "bash" || name == "shell" || name == "exec_command" || strings.HasSuffix(name, ".exec_command")
}

func legacyShellCommandFromJSON(input string) string {
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(input), &object) != nil {
		return ""
	}
	for _, key := range []string{"cmd", "command"} {
		var command string
		if raw, ok := object[key]; ok && json.Unmarshal(raw, &command) == nil {
			return command
		}
	}
	return ""
}

func legacyJSStringProperties(source, method string, keys ...string) []string {
	wrapper := "(async function(){\n" + source + "\n})()"
	program, err := jsparser.ParseFile(nil, "legacy-transcript.js", wrapper, 0)
	if err != nil || len(program.Body) != 1 {
		return nil
	}
	statement, ok := program.Body[0].(*jsast.ExpressionStatement)
	if !ok {
		return nil
	}
	invocation, ok := statement.Expression.(*jsast.CallExpression)
	if !ok {
		return nil
	}
	function, ok := invocation.Callee.(*jsast.FunctionLiteral)
	if !ok || function.Body == nil {
		return nil
	}
	calls := directLegacyJSCalls(function.Body.List)
	keySet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		keySet[key] = struct{}{}
	}
	var out []string
	for _, call := range calls {
		if !legacyJSToolMethod(call.Callee, method) || len(call.ArgumentList) != 1 {
			continue
		}
		object, ok := call.ArgumentList[0].(*jsast.ObjectLiteral)
		if !ok {
			continue
		}
		for _, property := range object.Value {
			keyed, ok := property.(*jsast.PropertyKeyed)
			if !ok || keyed.Computed {
				continue
			}
			key, ok := legacyJSKey(keyed.Key)
			if !ok {
				continue
			}
			if _, wanted := keySet[key]; !wanted {
				continue
			}
			value, ok := keyed.Value.(*jsast.StringLiteral)
			if ok {
				out = append(out, value.Value.String())
			}
		}
	}
	return out
}

func directLegacyJSCalls(statements []jsast.Statement) []*jsast.CallExpression {
	var calls []*jsast.CallExpression
	for _, statement := range statements {
		var expressions []jsast.Expression
		switch statement := statement.(type) {
		case *jsast.ExpressionStatement:
			expressions = append(expressions, statement.Expression)
		case *jsast.VariableStatement:
			for _, binding := range statement.List {
				if binding != nil {
					expressions = append(expressions, binding.Initializer)
				}
			}
		case *jsast.LexicalDeclaration:
			for _, binding := range statement.List {
				if binding != nil {
					expressions = append(expressions, binding.Initializer)
				}
			}
		}
		for _, expression := range expressions {
			calls = append(calls, directLegacyJSExpression(expression)...)
		}
	}
	return calls
}

func directLegacyJSExpression(expression jsast.Expression) []*jsast.CallExpression {
	switch expression := expression.(type) {
	case *jsast.AwaitExpression:
		return directLegacyJSExpression(expression.Argument)
	case *jsast.CallExpression:
		if legacyJSToolMethod(expression.Callee, "exec_command") || legacyJSToolMethod(expression.Callee, "wait") {
			return []*jsast.CallExpression{expression}
		}
		var calls []*jsast.CallExpression
		for _, argument := range expression.ArgumentList {
			calls = append(calls, directLegacyJSExpression(argument)...)
		}
		return calls
	case *jsast.ArrayLiteral:
		var calls []*jsast.CallExpression
		for _, value := range expression.Value {
			calls = append(calls, directLegacyJSExpression(value)...)
		}
		return calls
	case *jsast.SpreadElement:
		return directLegacyJSExpression(expression.Expression)
	default:
		return nil
	}
}

func legacyJSToolMethod(expression jsast.Expression, method string) bool {
	dot, ok := expression.(*jsast.DotExpression)
	if !ok || dot.Identifier.Name.String() != method {
		return false
	}
	tools, ok := dot.Left.(*jsast.Identifier)
	return ok && tools.Name.String() == "tools"
}

func legacyJSKey(expression jsast.Expression) (string, bool) {
	switch key := expression.(type) {
	case *jsast.StringLiteral:
		return key.Value.String(), true
	case *jsast.Identifier:
		return key.Name.String(), true
	default:
		return "", false
	}
}

func parseLegacyShellStatusCalls(command string) []legacyStatusCall {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "legacy-ticket-status")
	if err != nil {
		return nil
	}
	var calls []*syntax.CallExpr
	syntax.Walk(file, func(node syntax.Node) bool {
		if call, ok := node.(*syntax.CallExpr); ok {
			calls = append(calls, call)
		}
		return true
	})
	var out []legacyStatusCall
	for _, call := range calls {
		args := make([]string, len(call.Args))
		valid := true
		for i, word := range call.Args {
			args[i], valid = legacyShellWord(word, i == 0)
			if !valid {
				break
			}
		}
		if !valid || len(args) < 4 || !legacyAttnExecutable(args[0]) || args[1] != "ticket" || args[2] != "status" {
			continue
		}
		state := ""
		explicit := false
		for i := 3; i < len(args); i++ {
			switch {
			case args[i] == "--ticket":
				explicit = true
				i++
			case strings.HasPrefix(args[i], "--ticket="):
				explicit = true
			case legacyNormalizedStatus(args[i]) != "":
				if state != "" {
					state = ""
					i = len(args)
					continue
				}
				state = legacyNormalizedStatus(args[i])
			}
		}
		if state == "" {
			continue
		}
		simple := len(file.Stmts) == 1 && file.Stmts[0].Cmd == call && len(file.Stmts[0].Redirs) == 0 &&
			!file.Stmts[0].Negated && !file.Stmts[0].Background && len(calls) == 1
		out = append(out, legacyStatusCall{state: state, explicit: explicit, simple: simple})
	}
	return out
}

func legacyNormalizedStatus(value string) string {
	switch value {
	case "in_progress":
		return "working"
	case "needs_input":
		return "blocked"
	case "ready_for_review":
		return "in_review"
	case "completed", "done":
		return "done"
	case "failed":
		return "failed"
	case "crashed":
		return "crashed"
	default:
		return ""
	}
}

func legacyShellWord(word *syntax.Word, executable bool) (string, bool) {
	var builder strings.Builder
	for _, part := range word.Parts {
		switch part := part.(type) {
		case *syntax.Lit:
			builder.WriteString(part.Value)
		case *syntax.SglQuoted:
			builder.WriteString(part.Value)
		case *syntax.DblQuoted:
			for _, quoted := range part.Parts {
				switch quoted := quoted.(type) {
				case *syntax.Lit:
					builder.WriteString(quoted.Value)
				case *syntax.ParamExp:
					if !executable || quoted.Param == nil || quoted.Param.Value != "ATTN_WRAPPER_PATH" {
						return "", false
					}
					builder.WriteString("$ATTN_WRAPPER_PATH")
				default:
					return "", false
				}
			}
		case *syntax.ParamExp:
			if !executable || part.Param == nil || part.Param.Value != "ATTN_WRAPPER_PATH" {
				return "", false
			}
			builder.WriteString("$ATTN_WRAPPER_PATH")
		default:
			return "", false
		}
	}
	return builder.String(), true
}

func legacyAttnExecutable(value string) bool {
	return value == "$ATTN_WRAPPER_PATH" || filepath.Base(value) == "attn"
}

type legacyConversationTurn struct {
	role string
	text string
}

func renderLegacyConversation(source LegacyRecoverySource, records [][]byte) (string, string) {
	var strict, fallback []legacyConversationTurn
	var response []legacyConversationTurn
	for _, record := range records {
		var envelope eventEnvelope
		if json.Unmarshal(record, &envelope) != nil {
			continue
		}
		parsed := parseEventLine(source.Provider, record)
		for _, event := range parsed.events {
			if event.Kind != EventKindUser && event.Kind != EventKindAssistant {
				continue
			}
			role := event.Role
			text := strings.TrimSpace(event.Text)
			if text == "" {
				continue
			}
			if source.Provider == "codex" && envelope.Type == "response_item" {
				response = appendConversationTurn(response, legacyConversationTurn{role: role, text: text})
				continue
			}
			strict = appendConversationTurn(strict, legacyConversationTurn{role: role, text: text})
		}
		if source.Provider == "claude" && envelope.Type == "user" && envelope.Origin == nil {
			var message eventMessage
			if json.Unmarshal(envelope.Message, &message) == nil {
				text := strings.TrimSpace(extractEventContentText(message.Content))
				if text != "" {
					fallback = appendConversationTurn(fallback, legacyConversationTurn{role: "user", text: text})
				}
			}
		}
	}
	turns := strict
	if len(turns) == 0 {
		turns = response
	}
	if source.Provider == "claude" {
		hasHuman := false
		for _, item := range turns {
			hasHuman = hasHuman || item.role == "user"
		}
		if !hasHuman && len(fallback) > 0 {
			turns = append(fallback, turns...)
		}
	}
	firstHuman := ""
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Recovered conversation\n\nProvider: %s  \nSession: %s\n", source.Provider, source.NativeSessionID)
	for _, item := range turns {
		if item.role == "user" && firstHuman == "" {
			firstHuman = item.text
		}
		heading := "Assistant"
		if item.role == "user" {
			heading = "User"
		}
		fmt.Fprintf(&builder, "\n## %s\n\n%s\n", heading, item.text)
	}
	return builder.String(), firstHuman
}

func appendConversationTurn(turns []legacyConversationTurn, item legacyConversationTurn) []legacyConversationTurn {
	if len(turns) > 0 && turns[len(turns)-1] == item {
		return turns
	}
	return append(turns, item)
}
