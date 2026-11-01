package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"nhooyr.io/websocket"
)

const syntheticSpeechWordsPerSecond = 516.0 / 143.144

type record struct {
	Provider    string   `json:"provider"`
	Case        string   `json:"case"`
	Round       int      `json:"round"`
	RecordingMs int      `json:"recording_ms"`
	WallMs      float64  `json:"wall_ms"`
	Spoken      string   `json:"spoken"`
	Refined     string   `json:"refined,omitempty"`
	Error       string   `json:"error,omitempty"`
	LimitMs     int      `json:"limit_ms,omitempty"`
	WordRatio   float64  `json:"word_ratio"`
	Failures    []string `json:"failures,omitempty"`
	KnownLoss   bool     `json:"known_loss,omitempty"`
}

func main() {
	providers := flag.String("providers", "claude,codex", "comma-separated dictation.refinement_agent values")
	rounds := flag.Int("rounds", 1, "runs of the corpus per provider")
	only := flag.String("case", "", "run only the corpus case with this id")
	out := flag.String("out", "", "JSONL file for every input/output record (required)")
	flag.Parse()
	if *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(strings.Split(*providers, ","), *rounds, *only, *out); err != nil {
		fmt.Fprintln(os.Stderr, "dictationrefineprobe:", err)
		os.Exit(1)
	}
}

func run(providers []string, rounds int, only, out string) error {
	ctx := context.Background()
	app, err := connect(ctx)
	if err != nil {
		return err
	}
	defer app.conn.Close(websocket.StatusNormalClosure, "")
	file, err := os.Create(out)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	failed := 0
	for _, provider := range providers {
		if err := app.setSetting(ctx, "dictation.refinement_agent", provider); err != nil {
			return err
		}
		for round := 1; round <= rounds; round++ {
			for _, c := range corpus {
				if only != "" && c.id != only {
					continue
				}
				rec := app.refine(ctx, provider, round, c)
				if err := encoder.Encode(rec); err != nil {
					return err
				}
				verdict := "PASS"
				switch {
				case rec.KnownLoss:
					verdict = "KNOWN"
				case len(rec.Failures) > 0:
					verdict = "FAIL"
					failed++
				}
				fmt.Printf("%s %-7s r%d %-28s %7.0f ms  words %.2f  %s\n", verdict, provider, round, c.id, rec.WallMs, rec.WordRatio, strings.Join(rec.Failures, "; "))
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d refinements lost or changed what was said; see %s", failed, out)
	}
	return nil
}

func (a *app) refine(ctx context.Context, provider string, round int, c corpusCase) record {
	recording := time.Duration(float64(len(strings.Fields(c.spoken))) / syntheticSpeechWordsPerSecond * float64(time.Second))
	rec := record{Provider: provider, Case: c.id, Round: round, RecordingMs: int(recording.Milliseconds()), Spoken: c.spoken}
	requestID := "probe-" + rand.Text()
	start := time.Now()
	result, err := a.request(ctx, map[string]any{
		"cmd": protocol.CmdDictationRefine, "request_id": requestID, "profile_id": a.profile,
		"raw_text": c.spoken, "recording_duration_ms": rec.RecordingMs, "format": c.format,
	}, func(event map[string]any) bool {
		return event["event"] == protocol.EventDictationRefineResult && event["request_id"] == requestID
	})
	rec.WallMs = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		rec.Failures = []string{err.Error()}
		return rec
	}
	var answer protocol.DictationRefineResultMessage
	raw, _ := json.Marshal(result)
	if err := json.Unmarshal(raw, &answer); err != nil {
		rec.Failures = []string{err.Error()}
		return rec
	}
	rec.Refined, rec.Error, rec.LimitMs = protocol.Deref(answer.RefinedText), protocol.Deref(answer.Error), protocol.Deref(answer.LimitMs)
	if !answer.Success {
		rec.Failures = []string{"no refinement: " + rec.Error}
		return rec
	}
	rec.WordRatio = float64(len(strings.Fields(rec.Refined))) / float64(len(strings.Fields(c.spoken)))
	rec.Failures = judge(c, rec.Refined, rec.WordRatio)
	rec.KnownLoss = len(rec.Failures) > 0 && slices.Contains(c.knownLossBy, provider)
	return rec
}

func judge(c corpusCase, refined string, ratio float64) []string {
	text := normalize(refined)
	var failures []string
	for _, p := range c.keep {
		found := 0
		for _, alternative := range p.any {
			found += occurrences(text, normalize(alternative))
		}
		if found != p.times {
			failures = append(failures, fmt.Sprintf("%q appears %d times, want %d", p.any[0], found, p.times))
		}
	}
	for _, gone := range c.drop {
		if strings.Contains(text, normalize(gone)) {
			failures = append(failures, fmt.Sprintf("%q should have been removed", gone))
		}
	}
	if c.minRatio > 0 && ratio < c.minRatio {
		failures = append(failures, fmt.Sprintf("kept %.2f of the words, want at least %.2f", ratio, c.minRatio))
	}
	return failures
}

func occurrences(text, padded string) int {
	count := 0
	for at := strings.Index(text, padded); at >= 0; {
		count++
		next := strings.Index(text[at+len(padded)-1:], padded)
		if next < 0 {
			break
		}
		at += len(padded) - 1 + next
	}
	return count
}

var (
	outsideWords     = regexp.MustCompile(`[^a-z0-9'./_+:-]+`)
	trailingPunctual = regexp.MustCompile(`[.:'-]+(\s|$)`)
)

func normalize(text string) string {
	text = strings.ToLower(strings.NewReplacer("’", "'", "‘", "'", "—", " ", "–", " ").Replace(text))
	text = outsideWords.ReplaceAllString(text, " ")
	text = trailingPunctual.ReplaceAllString(text, " ")
	return " " + strings.Join(strings.Fields(text), " ") + " "
}

type app struct {
	conn    *websocket.Conn
	profile string
}

func connect(ctx context.Context) (*app, error) {
	instance := strings.TrimSpace(os.Getenv("ATTN_INSTANCE"))
	if instance == "" || instance == "default" {
		return nil, fmt.Errorf("ATTN_INSTANCE=%q selects production; source a non-production instance shell first", instance)
	}
	token := config.BrowserHostToken()
	if token == "" {
		token = rand.Text()
		if err := os.WriteFile(filepath.Join(config.DataDir(), "browser-host-token"), []byte(token), 0o600); err != nil {
			return nil, err
		}
	}
	url := "ws://localhost:" + config.WSPortForInstance(instance) + "/ws"
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"tauri://localhost"}}})
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", url, err)
	}
	conn.SetReadLimit(64 << 20)
	a := &app{conn: conn}
	initial, err := a.request(ctx, map[string]any{
		"cmd": protocol.CmdClientHello, "client_kind": "tauri-app", "version": "protocol-" + protocol.ProtocolVersion,
		"capabilities": []string{}, "client_token": config.ClientToken(), "browser_host_token": token,
	}, func(event map[string]any) bool { return event["event"] == protocol.EventInitialState })
	if err != nil {
		return nil, err
	}
	if selected, _ := initial["selected_profile_id"].(string); selected != "" {
		a.profile = selected
	} else if profiles, _ := initial["profiles"].([]any); len(profiles) > 0 {
		a.profile, _ = profiles[0].(map[string]any)["id"].(string)
	}
	if a.profile == "" {
		return nil, fmt.Errorf("the daemon at %s reported no profile", url)
	}
	return a, nil
}

func (a *app) setSetting(ctx context.Context, key, value string) error {
	requestID := "probe-" + rand.Text()
	result, err := a.request(ctx, map[string]any{"cmd": protocol.CmdSetSetting, "key": key, "value": value, "request_id": requestID},
		func(event map[string]any) bool {
			return event["event"] == protocol.EventSettingsUpdated && event["request_id"] == requestID
		})
	if err != nil {
		return err
	}
	if ok, _ := result["success"].(bool); !ok {
		return fmt.Errorf("set %s=%s: %v", key, value, result["error"])
	}
	return nil
}

func (a *app) request(ctx context.Context, msg map[string]any, match func(map[string]any) bool) (map[string]any, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	if err := a.conn.Write(ctx, websocket.MessageText, body); err != nil {
		return nil, err
	}
	for {
		kind, data, err := a.conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		if kind != websocket.MessageText {
			continue
		}
		var event map[string]any
		if json.Unmarshal(data, &event) == nil && match(event) {
			return event, nil
		}
	}
}
