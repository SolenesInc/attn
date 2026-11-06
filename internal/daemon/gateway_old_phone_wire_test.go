package daemon_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/gateway"
)

var recordOldPhone = flag.Int("record-old-phone", 0, "write testdata/old_phones/api-<N>.jsonl for a new gateway api; a recording that exists stays frozen")

const oldPhonesDir = "testdata/old_phones"

// A recording freezes what a phone of one gateway api sends and the shape of what it reads back. Lines are
// {"phone": message} or {"home": shape} or {"home_output": terminal}. A $name string is one of the conversation's ids.
type oldPhoneLine struct {
	Phone      json.RawMessage `json:"phone,omitempty"`
	Home       json.RawMessage `json:"home,omitempty"`
	HomeOutput string          `json:"home_output,omitempty"`
}

type oldPhoneStep struct {
	since   int
	phone   string
	replies []string
}

const homeOutputReply = "output"

// oldPhoneSteps feeds -record-old-phone only. A new api adds its commands here with since set to it.
var oldPhoneSteps = []oldPhoneStep{
	{1, `{"cmd":"hello","api":$api,"device_id":"phone"}`, []string{"welcome", "sessions"}},
	{3, ``, []string{"ask", "asks"}},
	{6, ``, []string{"garden_seeds", "garden"}},
	{2, `{"cmd":"ping"}`, []string{"pong"}},
	{3, `{"cmd":"ask_image","ask_id":"$ask","src":"missing.png","offset":0,"request_id":"image"}`, []string{"ask_image_error"}},
	{4, `{"cmd":"submit_prompt","session_id":"$session","text":"Yes, land it.","request_id":"submit"}`, []string{"submit_result"}},
	{4, `{"cmd":"terminal_attach","terminal_id":"$terminal","cols":49,"rows":35,"credit":262144}`, []string{"terminal_screen"}},
	{4, `{"cmd":"terminal_focus","terminal_id":"$terminal"}`, nil},
	{4, `{"cmd":"terminal_input","terminal_id":"$terminal","data":"echo from-the-phone\n"}`, []string{homeOutputReply}},
	{4, `{"cmd":"terminal_credit","terminal_id":"$terminal","bytes":4096}`, nil},
	{4, `{"cmd":"terminal_resize","terminal_id":"$terminal","cols":60,"rows":30}`, []string{"terminal_resized"}},
	{4, `{"cmd":"terminal_detach","terminal_id":"$terminal"}`, nil},
	{3, `{"cmd":"ask_answer","ask_id":"$ask","answers":[{"key":"q1","picked":["Postgres"]}],"note":"from the phone","request_id":"answer"}`, []string{"ask_answer_result"}},
	{6, `{"cmd":"seed_get","seed_id":"$seed","request_id":"read"}`, []string{"seed_detail"}},
	{6, `{"cmd":"seed_note","seed_id":"$seed","body":"from the phone","request_id":"note"}`, []string{"seed_result"}},
	{7, ``, []string{"seed_changed"}},
	{6, `{"cmd":"seed_move","seed_id":"$seed","verb":"park","text":"later","request_id":"move"}`, []string{"seed_result"}},
}

type oldPhoneConversation struct {
	t     *testing.T
	phone *testPhone
	ids   map[string]string
	read  [][]byte
}

func startOldPhoneConversation(t *testing.T) *oldPhoneConversation {
	t.Helper()
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "phone_access_enabled", "true")
	setSetting(t, app, "asks_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(session)
	app.AwaitScreen(session, "? for shortcuts")
	shell := w.Spawn(app, shellHarness, w.Path("shell"))
	asked, err := cli.AskOpen(protocol.SessionID(session), "", cacheAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	planted, err := cli.SeedPlant(protocol.SessionID(session), "Ship the phone", "The phone reads the Garden.", "", "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return &oldPhoneConversation{
		t: t, phone: connectPhone(t, w, "phone", app.SelectedProfile()),
		ids: map[string]string{"$session": session, "$terminal": app.Terminal(shell), "$ask": asked.ID, "$seed": planted.Seed.ID},
	}
}

func (c *oldPhoneConversation) send(message string) {
	c.t.Helper()
	for name, id := range c.ids {
		message = strings.ReplaceAll(message, name, id)
	}
	c.phone.send(json.RawMessage(message))
}

// await consumes the first frame the phone has read, or now reads, that found accepts.
func (c *oldPhoneConversation) await(what string, found func(frame []byte) string) []byte {
	c.t.Helper()
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	closest := "nothing like it arrived"
	take := func(frame []byte) bool {
		mismatch := found(frame)
		if mismatch == "" {
			return true
		}
		if mismatch != "another kind" {
			closest = fmt.Sprintf("%s in %s", mismatch, frame)
		}
		return false
	}
	for i, frame := range c.read {
		if take(frame) {
			c.read = slices.Delete(c.read, i, i+1)
			return frame
		}
	}
	for {
		select {
		case frame := <-c.phone.fromHome:
			if take(frame) {
				return frame
			}
			c.read = append(c.read, frame)
		case <-guard.C:
			c.t.Fatalf("a phone of this api waits for %s, and after fakeagent.HangGuard=%s: %s", what, fakeagent.HangGuard, closest)
		}
	}
}

func eventOf(frame []byte) string {
	if len(frame) == 0 || frame[0] != '{' {
		return ""
	}
	var head struct {
		Event string `json:"event"`
	}
	_ = json.Unmarshal(frame, &head)
	return head.Event
}

func (c *oldPhoneConversation) awaitOutput(terminal string) {
	c.t.Helper()
	c.await("output of terminal "+terminal, func(frame []byte) string {
		if frame[0] != protocol.BinaryFrameTypePtyOutput {
			return "another kind"
		}
		id, _, _, err := protocol.DecodePtyOutputFrame(frame)
		if err != nil {
			return "an output frame that does not decode: " + err.Error()
		}
		if string(id) != terminal {
			return "another kind"
		}
		return ""
	})
}

var frozenExactly = map[string]bool{"event": true, "cmd": true, "request_id": true, "api": true, "delivered": true}

// shapeMismatch names where got breaks what a phone that read want relies on. New fields never break it.
func (c *oldPhoneConversation) shapeMismatch(path string, want, got any) string {
	key := path[strings.LastIndex(path, ".")+1:]
	switch want := want.(type) {
	case map[string]any:
		object, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s is %T, want an object", path, got)
		}
		if _, failed := object["error"]; failed && want["error"] == nil {
			return fmt.Sprintf("%s reports an error", path)
		}
		for field, value := range want {
			present, ok := object[field]
			if !ok {
				return fmt.Sprintf("%s.%s is missing", path, field)
			}
			if mismatch := c.shapeMismatch(path+"."+field, value, present); mismatch != "" {
				return mismatch
			}
		}
	case []any:
		list, ok := got.([]any)
		if !ok {
			return fmt.Sprintf("%s is %T, want a list", path, got)
		}
		for _, shape := range want {
			mismatch := fmt.Sprintf("%s is empty", path)
			for i, element := range list {
				if mismatch = c.shapeMismatch(fmt.Sprintf("%s[%d]", path, i), shape, element); mismatch == "" {
					break
				}
			}
			if mismatch != "" {
				return mismatch
			}
		}
	case string:
		text, ok := got.(string)
		if !ok {
			return fmt.Sprintf("%s is %T, want a string", path, got)
		}
		id, named := c.ids[want]
		if named && text != id || !named && frozenExactly[key] && text != want {
			return fmt.Sprintf("%s is %q, want %q", path, text, want)
		}
	case float64:
		number, ok := got.(float64)
		if !ok {
			return fmt.Sprintf("%s is %T, want a number", path, got)
		}
		if frozenExactly[key] && number != want {
			return fmt.Sprintf("%s is %v, want %v", path, number, want)
		}
	case bool:
		value, ok := got.(bool)
		if !ok {
			return fmt.Sprintf("%s is %T, want a boolean", path, got)
		}
		if frozenExactly[key] && value != want {
			return fmt.Sprintf("%s is %v, want %v", path, value, want)
		}
	}
	return ""
}

func (c *oldPhoneConversation) awaitShape(line json.RawMessage) {
	c.t.Helper()
	var want map[string]any
	if err := json.Unmarshal(line, &want); err != nil {
		c.t.Fatalf("recording line %s: %v", line, err)
	}
	event, _ := want["event"].(string)
	c.await(string(line), func(frame []byte) string {
		if eventOf(frame) != event {
			return "another kind"
		}
		var got any
		if err := json.Unmarshal(frame, &got); err != nil {
			return err.Error()
		}
		return c.shapeMismatch(event, want, got)
	})
}

func readOldPhone(t *testing.T, path string) []oldPhoneLine {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var lines []oldPhoneLine
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		var line oldPhoneLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("%s: %v in %s", path, err, scanner.Bytes())
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

func recordedOldPhones(t *testing.T) map[int]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(oldPhonesDir, "api-*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	recorded := map[int]string{}
	for _, path := range paths {
		api, err := strconv.Atoi(regexp.MustCompile(`\d+`).FindString(filepath.Base(path)))
		if err != nil {
			t.Fatal(err)
		}
		recorded[api] = path
	}
	return recorded
}

func TestAPhoneOfEveryGatewayAPIKeepsWorkingAgainstThisHome(t *testing.T) {
	if *recordOldPhone != 0 {
		recordOldPhoneConversation(t, *recordOldPhone)
		return
	}
	recorded := recordedOldPhones(t)
	if recorded[gateway.APIVersion] == "" {
		t.Fatalf("the gateway api is %d and %s has no api-%d.jsonl. Add the new commands to oldPhoneSteps, then run: go test ./internal/daemon -run TestAPhoneOfEveryGatewayAPI -record-old-phone=%d", gateway.APIVersion, oldPhonesDir, gateway.APIVersion, gateway.APIVersion)
	}
	for api := 1; api <= gateway.APIVersion; api++ {
		path := recorded[api]
		if path == "" {
			t.Errorf("%s has no recording of api %d, and recordings are never removed", oldPhonesDir, api)
			continue
		}
		t.Run(fmt.Sprintf("api %d", api), func(t *testing.T) {
			conversation := startOldPhoneConversation(t)
			for _, line := range readOldPhone(t, path) {
				switch {
				case line.Phone != nil:
					conversation.send(string(line.Phone))
				case line.Home != nil:
					conversation.awaitShape(line.Home)
				case line.HomeOutput != "":
					conversation.awaitOutput(conversation.ids[line.HomeOutput])
				}
			}
		})
	}
}

func frozenShape(key string, value any, ids map[string]string) any {
	switch value := value.(type) {
	case map[string]any:
		shape := map[string]any{}
		for field, inner := range value {
			shape[field] = frozenShape(field, inner, ids)
		}
		return shape
	case []any:
		shapes := []any{}
		seen := map[string]bool{}
		for _, element := range value {
			shape := frozenShape(key, element, ids)
			encoded, err := json.Marshal(shape)
			if err != nil {
				panic(err)
			}
			if !seen[string(encoded)] {
				seen[string(encoded)] = true
				shapes = append(shapes, shape)
			}
		}
		return shapes
	case string:
		for name, id := range ids {
			if value == id {
				return name
			}
		}
		if frozenExactly[key] {
			return value
		}
		return "text"
	case float64:
		if frozenExactly[key] {
			return value
		}
		return 0
	case bool:
		if frozenExactly[key] {
			return value
		}
		return false
	}
	return value
}

func recordOldPhoneConversation(t *testing.T, api int) {
	path := filepath.Join(oldPhonesDir, fmt.Sprintf("api-%d.jsonl", api))
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%s exists, and a recording stays frozen: old phones still send what it says", path)
	}
	conversation := startOldPhoneConversation(t)
	var out bytes.Buffer
	write := func(line oldPhoneLine) {
		encoded, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(append(encoded, '\n'))
	}
	for _, step := range oldPhoneSteps {
		if step.since > api {
			continue
		}
		if step.phone != "" {
			message := strings.ReplaceAll(step.phone, "$api", strconv.Itoa(api))
			write(oldPhoneLine{Phone: json.RawMessage(message)})
			conversation.send(message)
		}
		for _, reply := range step.replies {
			if reply == homeOutputReply {
				conversation.awaitOutput(conversation.ids["$terminal"])
				write(oldPhoneLine{HomeOutput: "$terminal"})
				continue
			}
			frame := conversation.await(reply, func(frame []byte) string {
				if eventOf(frame) != reply {
					return "another kind"
				}
				return ""
			})
			var value any
			if err := json.Unmarshal(frame, &value); err != nil {
				t.Fatal(err)
			}
			shape, err := json.Marshal(frozenShape("", value, conversation.ids))
			if err != nil {
				t.Fatal(err)
			}
			write(oldPhoneLine{Home: shape})
		}
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
