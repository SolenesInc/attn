package classifier

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/victorarias/attn/internal/prompts"
)

const VerdictParked = "parked"

const VerdictSchema = `{"type":"object","properties":{"verdict":{"type":"string","enum":["WAITING","DONE","PARKED"]}},"required":["verdict"],"additionalProperties":false}`

func ComposeYieldInput(lastMessage string, runningBackgroundTasks []string) string {
	return fmt.Sprintf(
		"%s\n\n[harness facts] Still running when the turn ended: %s.",
		lastMessage,
		strings.Join(runningBackgroundTasks, "; "),
	)
}

const MaxTurns = 2

const DefaultClaudeClassifierModel = "haiku"

var verdictLineRegex = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`(?i)^\s*(?:VERDICT\s*[:=]\s*)?(WAITING_INPUT|WAITING|DONE|IDLE|PARKED)(?:\s*(?:[-:]\s+.*|\([^)]*\)|[.!?]))?\s*$`)
})

const classifierLogSnippetMaxChars = 600

const DefaultCodexClassifierModel = "gpt-5.6-luna"

func BuildPrompt(text string) string {
	return prompts.RenderText("turn-classifier", "classify", prompts.Values{"message": text})
}

const QuietStopVerdictSchema = `{"type":"object","properties":{"verdict":{"type":"string","enum":["READ","NOTHING"]}},"required":["verdict"],"additionalProperties":false}`

const (
	QuietStopRead    = "read"
	QuietStopNothing = "nothing"
)

func BuildQuietStopPrompt(text string) string {
	return prompts.RenderText("turn-classifier", "quiet-stop", prompts.Values{"message": text})
}

func ParseQuietStopVerdict(structuredOutput json.RawMessage, text string) (string, bool) {
	answer := []byte(structuredOutput)
	if len(answer) == 0 {
		answer = []byte(strings.TrimSpace(text))
	}
	var parsed struct {
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal(answer, &parsed); err != nil {
		return "", false
	}
	switch parsed.Verdict {
	case "READ":
		return QuietStopRead, true
	case "NOTHING":
		return QuietStopNothing, true
	default:
		return "", false
	}
}

func parseVerdictToken(value string) (string, bool) {
	match := verdictLineRegex().FindStringSubmatch(strings.TrimSpace(value))
	if len(match) < 2 {
		return "", false
	}

	switch strings.ToUpper(match[1]) {
	case "WAITING", "WAITING_INPUT":
		return "waiting_input", true
	case "DONE", "IDLE":
		return "idle", true
	case "PARKED":
		return VerdictParked, true
	default:
		return "", false
	}
}

func parseVerdictFromValue(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return parseVerdictToken(typed)
	case map[string]any:
		for _, key := range []string{"verdict", "state", "status"} {
			if raw, ok := typed[key]; ok {
				if result, ok := parseVerdictFromValue(raw); ok {
					return result, true
				}
			}
		}
		if raw, ok := typed["classification"]; ok {
			if result, ok := parseVerdictFromValue(raw); ok {
				return result, true
			}
		}
		if raw, ok := typed["needs_input"]; ok {
			if needsInput, ok := raw.(bool); ok {
				if needsInput {
					return "waiting_input", true
				}
				return "idle", true
			}
		}
	}
	return "", false
}

func parseVerdictFromJSONResponse(response string) (string, bool) {
	trimmed := strings.TrimSpace(response)
	if trimmed == "" {
		return "", false
	}

	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
		return parseVerdictFromValue(parsed)
	}

	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(trimmed[start:end+1]), &parsed); err == nil {
			return parseVerdictFromValue(parsed)
		}
	}

	return "", false
}

func parseVerdictFromResponse(response string) (string, bool) {
	if result, ok := parseVerdictFromJSONResponse(response); ok {
		return result, true
	}

	for _, line := range strings.Split(strings.TrimSpace(response), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if result, ok := parseVerdictToken(trimmed); ok {
			return result, true
		}
	}

	return "", false
}

func truncateForLog(value string, maxChars int) string {
	if maxChars <= 0 || len(value) <= maxChars {
		return value
	}
	return value[:maxChars] + "...(truncated)"
}

func ParseVerdict(structuredOutput json.RawMessage) (string, bool) {
	if len(structuredOutput) == 0 {
		return "", false
	}
	result, ok := parseVerdictFromJSONResponse(string(structuredOutput))
	if ok {
		DefaultLogger("classifier: parsed result from structured output: %s", result)
	}
	return result, ok
}

func ParseVerdictText(text string) (string, bool) {
	return parseVerdictFromResponse(text)
}

func TruncateForLog(value string) string {
	return truncateForLog(value, classifierLogSnippetMaxChars)
}

type LogFunc func(format string, args ...interface{})

var DefaultLogger LogFunc = func(format string, args ...interface{}) {}

func SetLogger(fn LogFunc) {
	DefaultLogger = fn
}
