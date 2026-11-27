package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/diag"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

const (
	analyticsSampleKind        = "analytics_sample"
	analyticsSampleInterval    = 20 * time.Minute
	analyticsSampleTimeout     = time.Minute
	appPerformanceReplyTimeout = 10 * time.Second
)

type analyticsSample struct {
	diag.ProcessSample
	DatabaseBytes    int64             `json:"database_bytes"`
	BusRetainedFacts int64             `json:"bus_retained_facts"`
	ConnectedClients int               `json:"connected_clients"`
	Workload         analyticsWorkload `json:"workload"`
	Processes        []diag.RoleMemory `json:"processes"`
}

type analyticsWorkload struct {
	Sessions   int `json:"sessions"`
	Agents     int `json:"agents"`
	Terminals  int `json:"terminals"`
	PanesDrawn int `json:"panes_drawn"`
	Desktops   int `json:"desktops"`
	Seeds      int `json:"seeds"`
}

func (d *Daemon) analyticsSampleHandler(ctx context.Context, _ *jobs.Job) (any, error) {
	if !d.analyticsEnabled() {
		return nil, nil
	}
	sample := analyticsSample{ProcessSample: diag.Sample(), ConnectedClients: d.wsHub.ClientCount()}
	var err error
	if sample.DatabaseBytes, err = d.store.DatabaseBytes(); err != nil {
		return nil, fmt.Errorf("sampling the database size: %w", err)
	}
	earliest, head, err := d.store.BusBounds()
	if err != nil {
		return nil, fmt.Errorf("sampling the bus size: %w", err)
	}
	if head > 0 {
		sample.BusRetainedFacts = head - earliest + 1
	}
	apps := d.connectedApps()
	if sample.Workload, err = d.sampleWorkload(ctx, apps); err != nil {
		return nil, err
	}
	sample.Processes = d.sampleProcessMemory(ctx, apps)
	for i, process := range sample.Processes {
		if process.Role == diag.RoleApp && process.Shell == nativeAppShell {
			sample.Processes[i].Performance = d.nativeAppPerformance(apps)
		}
	}
	return nil, d.appendAnalytics(analytics.StreamSample, "process", d.run.ID, sample)
}

type connectedApps struct {
	processes []appProcess
	drawn     map[protocol.TerminalID]struct{}
}

const nativeAppShell = "native"

type appProcess struct {
	pid    int
	client *wsClient
}

func (d *Daemon) connectedApps() connectedApps {
	apps := connectedApps{drawn: map[protocol.TerminalID]struct{}{}}
	d.wsHub.ForEachClient(func(client *wsClient) {
		if !client.isNativeApp() {
			return
		}
		app := appProcess{pid: client.processID(), client: client}
		if app.pid > 0 && !slices.ContainsFunc(apps.processes, func(seen appProcess) bool { return seen.pid == app.pid }) {
			apps.processes = append(apps.processes, app)
		}
		for _, terminal := range client.attachedTerminals() {
			apps.drawn[terminal] = struct{}{}
		}
	})
	return apps
}

func (d *Daemon) nativeAppPerformance(apps connectedApps) *protocol.AppPerformance {
	for _, app := range apps.processes {
		if !app.client.isTrustedAppClient() {
			continue
		}
		reply, err := d.appPerformanceRequests.ask(app.client, appPerformanceReplyTimeout, func(requestID string) bool {
			payload, err := json.Marshal(protocol.AppPerformanceRequestMessage{Event: protocol.EventAppPerformanceRequest, RequestID: requestID})
			return err == nil && d.sendOutbound(app.client, outboundMessage{kind: messageKindText, payload: payload})
		}, nil, nil)
		if err != nil {
			d.logf("analytics: asking the native app for its performance: %v", err)
		}
		return reply
	}
	return nil
}

func (d *Daemon) handleAppPerformanceResult(client *wsClient, msg *protocol.AppPerformanceResultMessage) {
	if !client.isTrustedAppClient() {
		d.sendCommandError(client, protocol.CmdAppPerformanceResult, "trusted app required")
		return
	}
	d.appPerformanceRequests.answer(client, msg.RequestID, &msg.Performance)
}

func (d *Daemon) sampleWorkload(ctx context.Context, apps connectedApps) (analyticsWorkload, error) {
	workload := analyticsWorkload{PanesDrawn: len(apps.drawn)}
	for _, session := range d.store.List("") {
		workload.Sessions++
		if session.Agent != protocol.AgentShellValue {
			workload.Agents++
		}
	}
	if d.ptyBackend != nil {
		workload.Terminals = len(d.ptyBackend.TerminalIDs(ctx))
	}
	var err error
	if workload.Desktops, err = d.store.CountDesktops(); err != nil {
		return analyticsWorkload{}, fmt.Errorf("sampling the desktops: %w", err)
	}
	seeds, err := d.seedsCollection()
	if err != nil {
		return analyticsWorkload{}, fmt.Errorf("sampling the garden size: %w", err)
	}
	if workload.Seeds, err = d.store.CountDocuments(*seeds); err != nil {
		return analyticsWorkload{}, fmt.Errorf("sampling the garden size: %w", err)
	}
	return workload, nil
}

func (d *Daemon) sampleProcessMemory(ctx context.Context, apps connectedApps) []diag.RoleMemory {
	roles := diag.RoleMemories{}
	if self, ok := diag.ReadProcess(os.Getpid()); ok {
		roles.Add(diag.RoleDaemon, self)
	}
	for _, pid := range d.ptyProcessIDs(ctx) {
		if process, ok := diag.ReadProcess(pid); ok {
			roles.Add(diag.RolePTYHost, process)
		}
	}
	for _, app := range apps.processes {
		if process, ok := diag.ReadProcess(app.pid); ok {
			roles.AddApp(nativeAppShell, process)
		}
		roles.AddWebKitProcesses(app.pid)
	}
	if err := roles.SplitWebContent(ctx); err != nil {
		d.logf("analytics: splitting WebContent memory: %v", err)
	}
	return roles.Sorted()
}

func (d *Daemon) ptyProcessIDs(ctx context.Context) []int {
	provider, ok := d.ptyBackend.(ptybackend.WorkerProcessProvider)
	if !ok {
		return nil
	}
	seen := map[int]struct{}{}
	for _, pid := range provider.WorkerPIDs(ctx) {
		seen[pid] = struct{}{}
	}
	return slices.Collect(maps.Keys(seen))
}
