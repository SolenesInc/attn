package daemon

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	browserDefaultWaitMs     = 5000
	browserDefaultScreenshot = "attn-browser.png"
	browserDefaultPDF        = "attn-browser.pdf"
)

func (d *Daemon) browserCommands() map[string]command.Handler {
	plain := func(action string) command.Handler {
		return command.Typed(func(ctx context.Context, c command.Caller, _ struct{}) (any, error) {
			return d.browse(ctx, c, browserControl{action: action})
		})
	}
	return map[string]command.Handler{
		"browser open":     command.Typed(d.runBrowserOpen),
		"browser snapshot": plain("snapshot"),
		"browser back":     plain("back"),
		"browser forward":  plain("forward"),
		"browser reload":   plain("reload"),
		"browser cookies": command.Typed(func(ctx context.Context, c command.Caller, _ struct{}) (any, error) {
			return d.browse(ctx, c, browserControl{action: "get_all_cookies", params: protocol.Ptr("{}")})
		}),
		"browser find":       command.Typed(d.runBrowserFind),
		"browser wait":       command.Typed(d.runBrowserWait),
		"browser click":      command.Typed(d.runBrowserClick),
		"browser type":       command.Typed(d.runBrowserType),
		"browser press":      command.Typed(d.runBrowserPress),
		"browser scroll":     command.Typed(d.runBrowserScroll),
		"browser command":    command.Typed(d.runBrowserCommand),
		"browser screenshot": command.Typed(d.runBrowserScreenshot),
		"browser pdf":        command.Typed(d.runBrowserPDF),
	}
}

func browserFromOutpost(ctx context.Context, wire string) error {
	if origin, remote := OriginOf(ctx); remote {
		return fmt.Errorf("%s is not supported from %s", wire, origin.Name)
	}
	return nil
}

func (d *Daemon) runBrowserOpen(ctx context.Context, c command.Caller, a command.BrowserOpenArgs) (any, error) {
	if err := browserFromOutpost(ctx, "open_browser"); err != nil {
		return nil, err
	}
	if err := d.openBrowser(a.URL, c.Session); err != nil {
		return nil, fmt.Errorf("open_browser: %w", err)
	}
	return protocol.BrowserOpenResult{URL: a.URL}, nil
}

func (d *Daemon) control(ctx context.Context, c command.Caller, ctl browserControl) (string, error) {
	if err := browserFromOutpost(ctx, "browser_control"); err != nil {
		return "", err
	}
	ctl.session = c.Session
	result := d.runBrowserControl(&ctl)
	if result.err != "" {
		return "", fmt.Errorf("browser_control: %s", result.err)
	}
	return result.data, nil
}

func (d *Daemon) browse(ctx context.Context, c command.Caller, ctl browserControl) (any, error) {
	data, err := d.control(ctx, c, ctl)
	if err != nil {
		return nil, err
	}
	return protocol.BrowserResult{Data: data}, nil
}

func encodedParams(params map[string]any) *string {
	encoded, _ := json.Marshal(params)
	return protocol.Ptr(string(encoded))
}

func locatorParams(using, value, name string) (map[string]any, error) {
	if strings.TrimSpace(using) == "" || strings.TrimSpace(value) == "" {
		return nil, command.Usage("{using} and {value} are required")
	}
	params := map[string]any{"using": using, "value": value}
	if name != "" {
		params["name"] = name
	}
	return params, nil
}

func (d *Daemon) runBrowserFind(ctx context.Context, c command.Caller, a command.BrowserFindArgs) (any, error) {
	params, err := locatorParams(a.Using, a.Value, a.Name)
	if err != nil {
		return nil, err
	}
	action := "find_element"
	if a.All {
		action = "find_elements"
	}
	return d.browse(ctx, c, browserControl{action: action, params: encodedParams(params)})
}

func (d *Daemon) runBrowserWait(ctx context.Context, c command.Caller, a command.BrowserWaitArgs) (any, error) {
	params, err := locatorParams(a.Using, a.Value, a.Name)
	if err != nil {
		return nil, err
	}
	params["state"] = cmp.Or(a.State, "attached")
	params["timeout"] = int64(browserDefaultWaitMs)
	if a.Timeout != nil {
		params["timeout"] = *a.Timeout
	}
	return d.browse(ctx, c, browserControl{action: "wait_for", params: encodedParams(params)})
}

func selectorOrElement(selector, element string) (string, string, error) {
	selector, element = strings.TrimSpace(selector), strings.TrimSpace(element)
	if selector == "" && element == "" {
		return "", "", command.Usage("{selector} <css> or {element} <id> is required")
	}
	return selector, element, nil
}

func (d *Daemon) runBrowserClick(ctx context.Context, c command.Caller, a command.BrowserClickArgs) (any, error) {
	selector, element, err := selectorOrElement(a.Selector, a.Element)
	if err != nil {
		return nil, err
	}
	if element == "" {
		return d.browse(ctx, c, browserControl{action: "click", selector: protocol.Ptr(selector)})
	}
	return d.browse(ctx, c, browserControl{action: "click_element", params: encodedParams(map[string]any{"element": a.Element})})
}

func (d *Daemon) runBrowserType(ctx context.Context, c command.Caller, a command.BrowserTypeArgs) (any, error) {
	selector, element, err := selectorOrElement(a.Selector, a.Element)
	if err != nil || a.Text == nil {
		return nil, command.Usage("{selector} <css> or {element} <id>, plus {text}, are required")
	}
	if element == "" {
		return d.browse(ctx, c, browserControl{action: "type", selector: protocol.Ptr(selector), text: a.Text})
	}
	if _, err := d.control(ctx, c, browserControl{action: "clear_element", params: encodedParams(map[string]any{"element": a.Element})}); err != nil {
		return nil, err
	}
	return d.browse(ctx, c, browserControl{action: "send_keys_to_element", params: encodedParams(map[string]any{"element": a.Element, "text": *a.Text})})
}

func (d *Daemon) runBrowserPress(ctx context.Context, c command.Caller, a command.BrowserPressArgs) (any, error) {
	if a.Text == "" {
		return nil, command.Usage("{text} <key> is required")
	}
	actions := []any{map[string]any{"type": "key", "id": "keyboard", "actions": []any{
		map[string]any{"type": "keyDown", "value": a.Text},
		map[string]any{"type": "keyUp", "value": a.Text},
	}}}
	return d.browse(ctx, c, browserControl{action: "perform_actions", params: encodedParams(map[string]any{"actions": actions})})
}

func (d *Daemon) runBrowserScroll(ctx context.Context, c command.Caller, a command.BrowserScrollArgs) (any, error) {
	actions := []any{map[string]any{"type": "wheel", "id": "wheel", "actions": []any{
		map[string]any{"type": "scroll", "deltaX": protocol.Deref(a.X), "deltaY": protocol.Deref(a.Y)},
	}}}
	return d.browse(ctx, c, browserControl{action: "perform_actions", params: encodedParams(map[string]any{"actions": actions})})
}

// browserToolMaxActionMs bounds the timeout a tool call may put in params, so
// the call answers within browserControlDefaultTimeout (20 s): the action's
// timeout plus browserControlTimeoutMargin (5 s). That is under the shortest
// tool-call limit of the harnesses that call attn's tools, pi-mcp 1.3.0's 30 s
// request default, below Claude Code's 60 s and Codex's 300 s.
const browserToolMaxActionMs = 15000

func browserParams(given string, wait bool) (*string, error) {
	params := cmp.Or(given, "{}")
	var object map[string]any
	if err := json.Unmarshal([]byte(params), &object); err != nil || object == nil {
		return nil, command.Usage("{params} must be a JSON object")
	}
	if timeout, ok := object["timeout"].(float64); ok && !wait && timeout > browserToolMaxActionMs {
		return nil, command.Usage("a tool call's {params} timeout is at most %d ms, so the call answers within %s; look again with a later call", browserToolMaxActionMs, browserControlDefaultTimeout)
	}
	return protocol.Ptr(params), nil
}

func (d *Daemon) runBrowserCommand(ctx context.Context, c command.Caller, a command.BrowserCommandArgs) (any, error) {
	params, err := browserParams(a.Params, a.Wait)
	if err != nil {
		return nil, err
	}
	return d.browse(ctx, c, browserControl{action: a.Action, params: params})
}

func (d *Daemon) runBrowserScreenshot(ctx context.Context, c command.Caller, a command.BrowserScreenshotArgs) (any, error) {
	return d.capture(ctx, c, browserControl{action: "screenshot"}, "PNG", a.Path, browserDefaultScreenshot)
}

func (d *Daemon) runBrowserPDF(ctx context.Context, c command.Caller, a command.BrowserPdfArgs) (any, error) {
	params, err := browserParams(a.Params, a.Wait)
	if err != nil {
		return nil, err
	}
	return d.capture(ctx, c, browserControl{action: "print_page", params: params}, "PDF", a.Path, browserDefaultPDF)
}

func (d *Daemon) capture(ctx context.Context, c command.Caller, ctl browserControl, kind, path, fallback string) (any, error) {
	path, _, err := d.callerPath(ctx, c, callerDestination("path", cmp.Or(path, fallback)))
	if err != nil {
		return nil, err
	}
	data, err := d.control(ctx, c, ctl)
	if err != nil {
		return nil, err
	}
	bytes, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, command.Refuse(command.CodeBadCapture, "decode %s: %v", kind, err)
	}
	if err := writePrivateFile(path, bytes); err != nil {
		return nil, command.Refuse(command.CodeWriteFailed, "write %s: %v", path, err)
	}
	return protocol.BrowserFileResult{Path: path}, nil
}

func writePrivateFile(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	written := temp.Chmod(0o600)
	if written == nil {
		_, written = temp.Write(data)
	}
	if closed := temp.Close(); written == nil {
		written = closed
	}
	if written == nil {
		written = os.Rename(temp.Name(), path)
	}
	if written != nil {
		_ = os.Remove(temp.Name())
	}
	return written
}
