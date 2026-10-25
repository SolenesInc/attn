package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
	"nhooyr.io/websocket"
)

type probe struct {
	ctx                 context.Context
	conn                *websocket.Conn
	write               sync.Mutex
	mu                  sync.Mutex
	changed             chan struct{}
	frames              []map[string]any
	outputs             map[string][]byte
	owners              map[string]string
	echo                string
	sent                map[int]time.Time
	latencies           []time.Duration
	floodBytes, desyncs int
	stalled             bool
	err                 error
}

var echoToken = regexp.MustCompile(`<E([0-9]{6})\n>`)

func (p *probe) send(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	p.write.Lock()
	defer p.write.Unlock()
	return p.conn.Write(p.ctx, websocket.MessageText, raw)
}
func (p *probe) read() {
	for {
		kind, raw, err := p.conn.Read(p.ctx)
		p.mu.Lock()
		if err != nil {
			p.err = err
			close(p.changed)
			p.mu.Unlock()
			return
		}
		reattach := ""
		if kind == websocket.MessageBinary {
			terminal, _, data, err := protocol.DecodePtyOutputFrame(raw)
			if err != nil {
				p.err = err
			} else {
				id := string(terminal)
				if output, watched := p.outputs[id]; watched {
					output = append(output, data...)
					if id == p.echo {
						for _, match := range echoToken.FindAllSubmatch(output, -1) {
							n, _ := strconv.Atoi(string(match[1]))
							if at, ok := p.sent[n]; ok {
								p.latencies = append(p.latencies, time.Since(at))
								delete(p.sent, n)
							}
						}
						if last := bytes.LastIndexByte(output, '>'); last >= 0 {
							output = output[last+1:]
						}
					}
					p.outputs[id] = output
				} else {
					p.floodBytes += len(data)
				}
			}
		} else {
			var frame map[string]any
			if err := json.Unmarshal(raw, &frame); err != nil {
				p.err = err
			} else {
				p.frames = append(p.frames, frame)
				if frame["event"] == "pty_desync" {
					p.desyncs++
					reattach, _ = frame["id"].(string)
				}
				if rows, ok := frame["outposts"].([]any); ok {
					for _, value := range rows {
						row, _ := value.(map[string]any)
						if row["state"] == "stalled" {
							p.stalled = true
						}
					}
				}
			}
		}
		close(p.changed)
		p.changed = make(chan struct{})
		p.mu.Unlock()
		if reattach != "" {
			if err := p.send(protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(reattach)}); err != nil {
				return
			}
		}
	}
}
func (p *probe) await(check func() bool) error {
	for {
		p.mu.Lock()
		done := check()
		changed, err := p.changed, p.err
		p.mu.Unlock()
		if done {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-changed:
		case <-p.ctx.Done():
			return p.ctx.Err()
		}
	}
}
func (p *probe) request(command any, event string, match func(map[string]any) bool) (map[string]any, error) {
	p.mu.Lock()
	mark := len(p.frames)
	p.mu.Unlock()
	if err := p.send(command); err != nil {
		return nil, err
	}
	var found map[string]any
	err := p.await(func() bool {
		for _, f := range p.frames[mark:] {
			if f["event"] == "command_error" {
				p.err = fmt.Errorf("command refused: %v", f)
				return false
			}
			if f["event"] == event && (match == nil || match(f)) {
				found = f
				return true
			}
		}
		return false
	})
	return found, err
}
func (p *probe) state(name, state string) error {
	return p.await(func() bool {
		for i := len(p.frames) - 1; i >= 0; i-- {
			rows, ok := p.frames[i]["outposts"].([]any)
			if !ok {
				continue
			}
			for _, value := range rows {
				row, _ := value.(map[string]any)
				if row["name"] == name {
					return row["state"] == state
				}
			}
		}
		return false
	})
}
func (p *probe) input(id, text string) error {
	return p.send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(id), Data: text})
}
func (p *probe) screen(id, text string) error {
	return p.await(func() bool { return bytes.Contains(p.outputs[id], []byte(text)) })
}
func (p *probe) attach(id string, watch bool) error {
	if watch {
		p.mu.Lock()
		p.outputs[id] = nil
		p.mu.Unlock()
	}
	result, err := p.request(protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(id)}, protocol.EventAttachResult, func(f map[string]any) bool { return f["id"] == id })
	if err != nil {
		return err
	}
	if result["success"] != true {
		return fmt.Errorf("attach: %v", result)
	}
	return nil
}
func (p *probe) spawn(host, cwd, from string) (string, error) {
	id := uuid.NewString()
	var placement *string
	if host != "" {
		placement = &host
	}
	var parent *protocol.SessionID
	if from != "" {
		parent = protocol.Ptr(protocol.SessionID(p.owners[from]))
	}
	result, err := p.request(protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: protocol.SessionID(id), ProfileID: "", Host: placement, SpawnedFrom: parent, Agent: protocol.AgentShellValue, Cwd: cwd, Cols: 100, Rows: 30}, protocol.EventSpawnResult, func(f map[string]any) bool { return f["id"] == id })
	if err != nil {
		return "", err
	}
	if result["success"] != true {
		return "", fmt.Errorf("spawn: %v", result)
	}
	var terminal string
	err = p.await(func() bool {
		for i := len(p.frames) - 1; i >= 0; i-- {
			desktops, ok := p.frames[i]["desktops"].([]any)
			if !ok {
				continue
			}
			for _, value := range desktops {
				desktop, _ := value.(map[string]any)
				panes, _ := desktop["panes"].([]any)
				for _, value := range panes {
					pane, _ := value.(map[string]any)
					if pane["session_id"] == id {
						terminal, _ = pane["runtime_id"].(string)
						return terminal != ""
					}
				}
			}
		}
		return false
	})
	if err == nil {
		p.owners[terminal] = id
	}
	return terminal, err
}
func (p *probe) measure(label string, n int) error {
	p.mu.Lock()
	p.latencies = nil
	p.sent = map[int]time.Time{}
	p.floodBytes = 0
	p.desyncs = 0
	p.stalled = false
	p.mu.Unlock()
	sentAt := make([]time.Time, n)
	began := time.Now()
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()
	for i := 0; i < n; i++ {
		<-ticker.C
		p.mu.Lock()
		sentAt[i] = time.Now()
		p.sent[i] = sentAt[i]
		p.mu.Unlock()
		trace := fmt.Sprintf("%s:%d", label, i)
		if err := p.send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(p.echo), Data: fmt.Sprintf("E%06d\n", i), TraceID: &trace}); err != nil {
			return err
		}
	}
	if err := p.await(func() bool { return len(p.latencies) == n }); err != nil {
		return err
	}
	p.mu.Lock()
	samples := append([]time.Duration(nil), p.latencies...)
	floodBytes, desyncs, stalled := p.floodBytes, p.desyncs, p.stalled
	p.mu.Unlock()
	slices.Sort(samples)
	raw, _ := json.Marshal(map[string]any{"mode": label, "samples": n, "p50_ms": float64(samples[n/2]) / float64(time.Millisecond), "p99_ms": float64(samples[(99*n+99)/100-1]) / float64(time.Millisecond), "max_ms": float64(samples[n-1]) / float64(time.Millisecond), "flood_MB_s": float64(floodBytes) / time.Since(began).Seconds() / 1e6, "reattaches_s": float64(desyncs) / time.Since(began).Seconds(), "heartbeat_stalled": stalled})
	fmt.Println(string(raw))
	snapshot, err := p.request(map[string]any{"cmd": "support_snapshot", "request_id": label, "runtime_ids": []string{p.echo}}, "support_snapshot_result", nil)
	if err != nil {
		return err
	}
	var queued, writes []float64
	for _, item := range snapshot["input_traces"].([]any) {
		trace := item.(map[string]any)
		key := strings.TrimPrefix(trace["trace_id"].(string), label+":")
		i, err := strconv.Atoi(key)
		if err != nil || i < 0 || i >= n {
			continue
		}
		queued = append(queued, trace["received_at_unix_ms"].(float64)-float64(sentAt[i].UnixMilli()))
		writes = append(writes, trace["write_duration_us"].(float64)/1000)
	}
	slices.Sort(queued)
	slices.Sort(writes)
	if len(queued) > 0 {
		raw, _ := json.Marshal(map[string]any{"mode": label + "-input-traces", "samples": len(queued), "home_queue_p50_ms": queued[len(queued)/2], "home_queue_p99_ms": queued[(99*len(queued)+99)/100-1], "bridge_write_p50_ms": writes[len(writes)/2], "bridge_write_p99_ms": writes[(99*len(writes)+99)/100-1]})
		fmt.Println(string(raw))
	}
	if stalled {
		return fmt.Errorf("heartbeat stalled during %s", label)
	}
	return nil
}
func run() error {
	url := flag.String("url", "", "isolated home's WebSocket URL")
	data := flag.String("data", "", "isolated home's data directory")
	address := flag.String("address", "", "outpost bridge address")
	name := flag.String("name", "ai-sandbox", "outpost name")
	cwd := flag.String("cwd", "/tmp", "outpost working directory")
	mode := flag.String("mode", "scenario", "scenario, direct, derp, remove, or local")
	cut := flag.String("cut-command", "", "local script that cuts and automatically restores the outpost route")
	samples := flag.Int("samples", 1000, "echo samples per mode")
	flag.Parse()
	if *url == "" || *data == "" {
		return fmt.Errorf("--url and --data must name an isolated home")
	}
	token, err := os.ReadFile(filepath.Join(*data, "client-token"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, _, err := websocket.Dial(ctx, *url, nil)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(-1)
	p := &probe{ctx: ctx, conn: conn, changed: make(chan struct{}), outputs: map[string][]byte{}, owners: map[string]string{}, sent: map[int]time.Time{}}
	go p.read()
	initial, err := p.request(protocol.ClientHelloMessage{Cmd: protocol.CmdClientHello, ClientKind: "tauri-app", Version: "protocol-" + protocol.ProtocolVersion, ClientToken: protocol.Ptr(strings.TrimSpace(string(token))), Capabilities: []string{protocol.CapabilityBinaryPtyOutput}}, protocol.EventInitialState, nil)
	if err != nil {
		return err
	}
	_ = initial
	if *mode == "local" {
		for range 2 {
			terminal, err := p.spawn("", *cwd, "")
			if err != nil {
				return err
			}
			if err := p.attach(terminal, true); err != nil {
				return err
			}
			if err := p.input(terminal, `exec /bin/bash -c 'printf "__local_host=%s\n" "${ATTN_HOST-unset}"; exit 1'`+"\r"); err != nil {
				return err
			}
			if err := p.screen(terminal, "__local_host=unset"); err != nil {
				return err
			}
		}
		if err := p.await(func() bool {
			count := 0
			for _, frame := range p.frames {
				if frame["event"] == "session_exited" {
					count++
				}
			}
			return count == 2
		}); err != nil {
			return err
		}
		fmt.Println("two local shells ran, ATTN_HOST unset, exit 1 retained; Outposts off")
		return nil
	}
	if err := p.await(func() bool {
		for _, f := range p.frames {
			if f["event"] == protocol.EventOutpostsUpdated {
				return true
			}
		}
		return false
	}); err != nil {
		return err
	}
	host := ""
	p.mu.Lock()
	for _, f := range p.frames {
		rows, _ := f["outposts"].([]any)
		for _, value := range rows {
			row, _ := value.(map[string]any)
			if row["name"] == *name {
				host, _ = row["id"].(string)
			}
		}
	}
	p.mu.Unlock()
	if host == "" {
		if err := p.send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: *name, Address: *address}); err != nil {
			return err
		}
		if err := p.state(*name, "connected"); err != nil {
			return err
		}
		p.mu.Lock()
		for _, f := range p.frames {
			rows, _ := f["outposts"].([]any)
			for _, value := range rows {
				row, _ := value.(map[string]any)
				if row["name"] == *name {
					host, _ = row["id"].(string)
				}
			}
		}
		p.mu.Unlock()
	} else {
		if err := p.state(*name, "connected"); err != nil {
			return err
		}
	}
	if *mode == "remove" {
		_, err := p.request(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: host}, protocol.EventOutpostsUpdated, func(f map[string]any) bool { rows, _ := f["outposts"].([]any); return len(rows) == 0 })
		return err
	}
	defer func() {
		_, err := p.request(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: host}, protocol.EventOutpostsUpdated, func(f map[string]any) bool { rows, _ := f["outposts"].([]any); return len(rows) == 0 })
		if err != nil {
			fmt.Fprintln(os.Stderr, "remove outpost:", err)
		} else {
			fmt.Println("removed outpost and closed its terminals")
		}
	}()
	shell, err := p.spawn(host, *cwd, "")
	if err != nil {
		return err
	}
	if err := p.attach(shell, true); err != nil {
		return err
	}
	if err := p.input(shell, "printf '__attn_host=%s system=%s\\n' \"$ATTN_HOST\" \"$(uname)\"\r"); err != nil {
		return err
	}
	if err := p.screen(shell, "__attn_host="+*name+" system=Linux"); err != nil {
		return err
	}
	split, err := p.spawn(host, *cwd, shell)
	if err != nil {
		return err
	}
	if err := p.attach(split, false); err != nil {
		return err
	}
	fmt.Printf("remote shells ready: host=%s terminal=%s split=%s cwd=%s\n", host, shell, split, *cwd)
	if *mode == "scenario" {
		if *cut != "" {
			command := exec.Command(*cut)
			command.Stdout, command.Stderr = os.Stdout, os.Stderr
			if err := command.Run(); err != nil {
				return err
			}
			if err := p.state(*name, "lost"); err != nil {
				return err
			}
			fmt.Println("route cut: lost")
			if err := p.state(*name, "connected"); err != nil {
				return err
			}
			if err := p.attach(shell, true); err != nil {
				return err
			}
			if err := p.input(shell, "printf '__route_restored=%s\\n' 4\r"); err != nil {
				return err
			}
			if err := p.screen(shell, "__route_restored=4"); err != nil {
				return err
			}
			fmt.Println("route restored: same shell answered")
		}
		return nil
	}
	startEcho := func(terminal string) error {
		p.mu.Lock()
		p.echo = terminal
		p.outputs[terminal] = nil
		p.mu.Unlock()
		if err := p.input(terminal, "stty raw -echo; python3 -u -c 'import os,sys; print(\"echo_\"+\"READY\",flush=True); [os.write(1,b\"<\"+line+b\">\") for line in sys.stdin.buffer]'\r"); err != nil {
			return err
		}
		return p.screen(terminal, "echo_READY")
	}
	if err := startEcho(shell); err != nil {
		return err
	}
	if err := p.measure(*mode+"-idle", *samples); err != nil {
		return err
	}
	if err := p.input(split, "yes flood\r"); err != nil {
		return err
	}
	if err := p.measure(*mode+"-one-flood", *samples); err != nil {
		return err
	}
	local, err := p.spawn("", os.TempDir(), "")
	if err != nil {
		return err
	}
	if err := p.attach(local, true); err != nil {
		return err
	}
	if err := startEcho(local); err != nil {
		return err
	}
	if err := p.measure(*mode+"-local-under-remote-flood", *samples); err != nil {
		return err
	}
	_, err = p.request(protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: protocol.SessionID(p.owners[local])}, protocol.EventSessionCloseResult, nil)
	return err
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
