package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/hub"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	browserTileID                  = "tile-browser"
	browserControlDefaultTimeout   = 20 * time.Second
	browserControlTimeoutMargin    = 5 * time.Second
	browserControlMaxActionTimeout = 120 * time.Second
)

type browserControlResult struct {
	data string
	err  string
}

type browserControlPending struct {
	host   *wsClient
	result chan browserControlResult
}

type browserTarget struct {
	desktopID string
	location  agentLocation
	layout    layouttree.Node
}

func browserControlTimeout(params map[string]any) (time.Duration, error) {
	raw, ok := params["timeout"]
	if !ok {
		return browserControlDefaultTimeout, nil
	}
	milliseconds, ok := raw.(float64)
	if !ok || milliseconds < 0 {
		return 0, fmt.Errorf("timeout must be a non-negative number of milliseconds")
	}
	if milliseconds > float64(browserControlMaxActionTimeout.Milliseconds()) {
		return 0, fmt.Errorf("timeout cannot exceed %d milliseconds", browserControlMaxActionTimeout.Milliseconds())
	}
	requested := time.Duration(milliseconds * float64(time.Millisecond))
	timeout := requested + browserControlTimeoutMargin
	if timeout < browserControlDefaultTimeout {
		timeout = browserControlDefaultTimeout
	}
	return timeout, nil
}

func (d *Daemon) browserHost() *wsClient {
	if d.wsHub == nil {
		return nil
	}
	return d.wsHub.NewestClientMatching(func(client *wsClient) bool {
		return client.IsBrowserHost()
	})
}

func (d *Daemon) sendBrowserHostRequest(host *wsClient, request *protocol.BrowserControlRequestMessage) bool {
	if host == nil {
		return false
	}
	payload, err := json.Marshal(request)
	if err != nil {
		d.logf("browser host request marshal failed: %v", err)
		return false
	}
	return d.sendOutbound(host, outboundMessage{kind: messageKindText, payload: payload})
}

func validateBrowserURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("only http and https urls are supported")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("url host is required")
	}
	return parsed.String(), nil
}

func (d *Daemon) browserTargetFor(sessionID protocol.SessionID) (browserTarget, error) {
	sessionID = protocol.TrimID(sessionID)
	if sessionID != "" && d.store.Get(sessionID) == nil && d.hubManager != nil && d.hubManager.RemoteSession(sessionID) != nil {
		return browserTarget{}, fmt.Errorf("agent %s runs on an outpost: %w", sessionID, hub.ErrOutpostsOff)
	}
	location, err := d.currentAgent(sessionID)
	if err != nil {
		return browserTarget{}, err
	}
	if location.desktopID == "" {
		return browserTarget{}, fmt.Errorf("profile %s has no current desktop to open the browser on", location.profileID)
	}
	desktop, err := d.store.GetDesktop(location.desktopID)
	if err != nil {
		return browserTarget{}, err
	}
	return browserTarget{
		desktopID: desktop.ID,
		location:  location,
		layout:    desktop.Tree,
	}, nil
}

func (d *Daemon) handleOpenBrowser(conn net.Conn, msg *protocol.OpenBrowserMessage) {
	targetURL, err := validateBrowserURL(msg.URL)
	if err != nil {
		d.sendError(conn, "open_browser: "+err.Error())
		return
	}
	target, err := d.browserTargetFor(protocol.Deref(msg.SessionID))
	if err != nil {
		d.sendError(conn, "open_browser: "+err.Error())
		return
	}
	alreadyOpen := browserTileOn(target.layout)
	if _, _, err := d.openAgentTile(target.location, agentTile{
		tileID:   browserTileID,
		tileKind: string(layouttree.TileKindBrowser),
		params:   targetURL,
	}); err != nil {
		d.sendError(conn, fmt.Sprintf("open_browser: %v", err))
		return
	}
	if alreadyOpen {
		d.sendBrowserNavigation(target, targetURL)
	}
	d.logf("open_browser: %s on desktop %s", targetURL, target.desktopID)
	d.sendOK(conn)
}

func (d *Daemon) sendBrowserNavigation(target browserTarget, targetURL string) {
	if d.wsHub == nil {
		return
	}
	request := &protocol.BrowserControlRequestMessage{
		Event:     protocol.EventBrowserControlRequest,
		RequestID: fmt.Sprintf("browser-open-%d", time.Now().UnixNano()),
		DesktopID: target.desktopID,
		TileID:    browserTileID,
		Action:    "navigate",
		Text:      protocol.Ptr(targetURL),
	}
	d.sendBrowserHostRequest(d.browserHost(), request)
}

func browserTileOn(layout layouttree.Node) bool {
	for _, tile := range layouttree.TileLeaves(layout) {
		if tile.TileID == browserTileID && tile.TileKind == string(layouttree.TileKindBrowser) {
			return true
		}
	}
	return false
}

func normalizeBrowserAction(action string) (string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "accept_alert", "add_cookie", "back", "clear_element", "click",
		"click_element", "delete_all_cookies", "delete_cookie", "dismiss_alert",
		"element_screenshot", "execute_async_script", "execute_script", "find_element",
		"find_element_from_element", "find_element_from_shadow", "find_elements",
		"find_elements_from_element", "find_elements_from_shadow", "forward",
		"get_active_element", "get_alert_text", "get_all_cookies", "get_cookie",
		"get_element_attribute", "get_element_computed_label", "get_element_computed_role",
		"get_element_css_value", "get_element_property", "get_element_rect",
		"get_element_shadow_root", "get_element_tag_name", "get_element_text",
		"get_source", "get_title", "get_url", "get_window_handle", "get_window_handles",
		"is_element_displayed", "is_element_enabled", "is_element_selected", "navigate",
		"perform_actions", "print_page", "release_actions", "reload", "screenshot",
		"select_option", "send_alert_text", "send_keys_to_element", "snapshot", "switch_to_frame",
		"switch_to_parent_frame", "check", "type", "wait_for":
		return action, nil
	default:
		return "", fmt.Errorf("unsupported action %q", action)
	}
}

func newBrowserControlRequestID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate browser control request id: %w", err)
	}
	return "browser-" + hex.EncodeToString(bytes), nil
}

func (d *Daemon) runBrowserControl(msg *protocol.BrowserControlMessage) browserControlResult {
	action, err := normalizeBrowserAction(msg.Action)
	if err != nil {
		return browserControlResult{err: err.Error()}
	}
	selector := strings.TrimSpace(protocol.Deref(msg.Selector))
	if (action == "click" || action == "type") && selector == "" {
		return browserControlResult{err: action + " requires --selector"}
	}
	if action == "type" && msg.Text == nil {
		return browserControlResult{err: "type requires --text"}
	}
	var params map[string]any
	if msg.Params != nil {
		if err := json.Unmarshal([]byte(*msg.Params), &params); err != nil {
			return browserControlResult{err: "params must be a JSON object: " + err.Error()}
		}
		if params == nil {
			return browserControlResult{err: "params must be a JSON object"}
		}
	}
	controlTimeout, err := browserControlTimeout(params)
	if err != nil {
		return browserControlResult{err: err.Error()}
	}
	if action == "navigate" {
		target := protocol.Deref(msg.Text)
		if value, ok := params["url"].(string); ok {
			target = value
		}
		targetURL, err := validateBrowserURL(target)
		if err != nil {
			return browserControlResult{err: "navigate: " + err.Error()}
		}
		msg.Text = protocol.Ptr(targetURL)
		if msg.Params != nil {
			params["url"] = targetURL
			normalized, marshalErr := json.Marshal(params)
			if marshalErr != nil {
				return browserControlResult{err: "navigate: " + marshalErr.Error()}
			}
			msg.Params = protocol.Ptr(string(normalized))
		}
	}

	target, err := d.browserTargetFor(protocol.Deref(msg.SessionID))
	if err != nil {
		return browserControlResult{err: err.Error()}
	}
	if !browserTileOn(target.layout) {
		return browserControlResult{err: "no browser tile is open for that session"}
	}

	return d.runLocalBrowserControl(target, action, selector, msg, controlTimeout)
}

func (d *Daemon) runLocalBrowserControl(
	target browserTarget,
	action string,
	selector string,
	msg *protocol.BrowserControlMessage,
	controlTimeout time.Duration,
) browserControlResult {
	host := d.browserHost()
	if host == nil {
		return browserControlResult{err: "no in-app browser host is connected"}
	}

	requestID, err := newBrowserControlRequestID()
	if err != nil {
		return browserControlResult{err: err.Error()}
	}
	resultCh := make(chan browserControlResult, 1)
	d.browserControlMu.Lock()
	if d.browserControl == nil {
		d.browserControl = make(map[string]browserControlPending)
	}
	d.browserControl[requestID] = browserControlPending{host: host, result: resultCh}
	d.browserControlMu.Unlock()
	defer func() {
		d.browserControlMu.Lock()
		delete(d.browserControl, requestID)
		d.browserControlMu.Unlock()
	}()

	request := &protocol.BrowserControlRequestMessage{
		Event:     protocol.EventBrowserControlRequest,
		RequestID: requestID,
		DesktopID: target.desktopID,
		TileID:    browserTileID,
		Action:    action,
		Params:    msg.Params,
	}
	if selector != "" {
		request.Selector = protocol.Ptr(selector)
	}
	if msg.Text != nil {
		request.Text = msg.Text
	}
	if !d.sendBrowserHostRequest(host, request) {
		return browserControlResult{err: "in-app browser host is unavailable"}
	}

	select {
	case result := <-resultCh:
		return result
	case <-time.After(controlTimeout):
		return browserControlResult{err: "timed out waiting for the in-app browser"}
	}
}

func (d *Daemon) handleBrowserControl(conn net.Conn, msg *protocol.BrowserControlMessage) {
	result := d.runBrowserControl(msg)
	if result.err != "" {
		d.sendError(conn, "browser_control: "+result.err)
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, Data: protocol.Ptr(result.data)})
}

func (d *Daemon) handleRemoteBrowserControl(client *wsClient, msg *protocol.BrowserControlMessage) {
	requestID := strings.TrimSpace(protocol.Deref(msg.RequestID))
	if requestID == "" {
		d.sendCommandError(client, protocol.CmdBrowserControl, "request_id is required")
		return
	}
	result := d.runBrowserControl(msg)
	response := &protocol.BrowserControlResponseMessage{
		Event:     protocol.EventBrowserControlResponse,
		RequestID: requestID,
		Success:   result.err == "",
	}
	if result.err != "" {
		response.Error = protocol.Ptr(result.err)
	} else {
		response.Data = protocol.Ptr(result.data)
	}
	d.sendToClient(client, response)
}

// failBrowserControlFor answers every request waiting on host once it disconnects, instead of letting each
// sit out its timeout (20s by default, up to the action's own budget).
func (d *Daemon) failBrowserControlFor(host *wsClient) {
	d.browserControlMu.Lock()
	defer d.browserControlMu.Unlock()
	for _, pending := range d.browserControl {
		if pending.host != host {
			continue
		}
		select {
		case pending.result <- browserControlResult{err: "the in-app browser host disconnected"}:
		default:
		}
	}
}

func (d *Daemon) handleBrowserControlResult(client *wsClient, msg *protocol.BrowserControlResultMessage) {
	if !client.IsBrowserHost() {
		d.sendCommandError(client, protocol.CmdBrowserControlResult, "trusted browser host required")
		return
	}
	requestID := strings.TrimSpace(msg.RequestID)
	d.browserControlMu.Lock()
	pending, ok := d.browserControl[requestID]
	d.browserControlMu.Unlock()
	if !ok || pending.host != client {
		return
	}
	result := browserControlResult{
		data: protocol.Deref(msg.Data),
		err:  protocol.Deref(msg.Error),
	}
	if !msg.Success && result.err == "" {
		result.err = "browser host action failed"
	}
	select {
	case pending.result <- result:
	default:
	}
}
