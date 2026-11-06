package daemon

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/diag"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/ptyhost"
)

const (
	analyticsSampleKind     = "analytics_sample"
	analyticsSampleInterval = 20 * time.Minute
	analyticsSampleTimeout  = time.Minute
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
	return nil, d.appendAnalytics(analytics.StreamSample, "process", d.run.ID, sample)
}

type connectedApps struct {
	pids  []int
	drawn map[protocol.TerminalID]struct{}
}

func (d *Daemon) connectedApps() connectedApps {
	apps := connectedApps{drawn: map[protocol.TerminalID]struct{}{}}
	d.wsHub.ForEachClient(func(client *wsClient) {
		if !client.isApp() {
			return
		}
		if pid := client.processID(); pid > 0 && !slices.Contains(apps.pids, pid) {
			apps.pids = append(apps.pids, pid)
		}
		for _, terminal := range client.attachedTerminals() {
			apps.drawn[terminal] = struct{}{}
		}
	})
	return apps
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
			roles.Add(ptyProcessRole(process), process)
		}
	}
	for _, pid := range apps.pids {
		if app, ok := diag.ReadProcess(pid); ok {
			roles.Add(diag.RoleApp, app)
		}
		roles.AddWebKitProcesses(pid)
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

func ptyProcessRole(process diag.Process) string {
	if strings.HasPrefix(process.Name, ptyhost.BinaryName) {
		return diag.RolePTYHost
	}
	return diag.RolePTYWorker
}
