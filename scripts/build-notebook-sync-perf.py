#!/usr/bin/env python3
from pathlib import Path
import argparse,hashlib,json,re,subprocess
parser=argparse.ArgumentParser(description='Build attn with temporary Notebook event, broadcast and conflict-walk counters.')
parser.add_argument('--out',required=True,type=Path)
args=parser.parse_args()
root=Path.cwd(); output=args.out.resolve();output.mkdir(parents=True,exist_ok=False)
replace={}
def overlay(name, transform):
 p=root/name;s=p.read_text();s=transform(s);target=output/Path(name).name;target.write_text(s);replace[str(p)]=str(target)
def notebook(s):
 s=s.replace('"github.com/victorarias/attn/internal/bridge"','"github.com/victorarias/attn/internal/bridge"\n"github.com/victorarias/attn/internal/diag"',1)
 s,n=re.subn(r'(case \w+, ok := <-sub.C\(\):)',r'\1\n diag.NotebookProbeEvents.Add(1)',s,count=1);assert n==1
 lines=[]; walks=0
 for line in s.splitlines():
  lines.append(line)
  if line.startswith('func (d *Daemon) notebookConflictCopies(root string)'):
   lines.append('diag.NotebookProbeWalks.Add(1)');walks+=1
 assert walks==1
 s='\n'.join(lines)+'\n'
 s=s.replace('func (d *Daemon) refreshNotebookSync(run *notebookSyncRun) {', 'func (d *Daemon) refreshNotebookSync(run *notebookSyncRun) {\n diag.NotebookProbeRefreshes.Add(1)',1)
 return s
def diag(s):
 s=s.replace('"runtime"','"runtime"\n"sync/atomic"\n"github.com/prometheus/client_golang/prometheus"',1)
 s=s.replace('type Stats struct {','var NotebookProbeRefreshes atomic.Uint64\nvar NotebookProbeEvents atomic.Uint64\nvar NotebookProbeWalks atomic.Uint64\nvar NotebookProbeBroadcasts atomic.Uint64\n\ntype Stats struct {',1)
 s=s.replace('"pid":', '"notebook_sync_refreshes": NotebookProbeRefreshes.Load(),\n"notebook_engine_events": NotebookProbeEvents.Load(),\n"notebook_conflict_walks": NotebookProbeWalks.Load(),\n"notebook_broadcasts": NotebookProbeBroadcasts.Load(),\n"pid":',1)
 s=s.replace('w.Header().Set("Content-Type", "application/json")', '''families, err := prometheus.DefaultGatherer.Gather()
 counts := map[string]float64{}
 if err == nil { for _, family := range families { if family.GetName() == "syncthing_events_total" { for _, metric := range family.Metric { for _, label := range metric.Label { if label.GetName() == "state" { counts[label.GetValue()] += metric.GetCounter().GetValue() } } } } } }
 out["notebook_probe_event_totals"] = counts
 w.Header().Set("Content-Type", "application/json")''',1)
 return s
def wire(s):
 s=s.replace('"github.com/victorarias/attn/internal/bridge"','"github.com/victorarias/attn/internal/bridge"\n"github.com/victorarias/attn/internal/diag"',1)
 needle='d.wsHub.BroadcastValue(d.outpostsUpdatedMessage())';assert s.count(needle)==1
 return s.replace(needle,'diag.NotebookProbeBroadcasts.Add(1); '+needle,1)
overlay('internal/daemon/notebook_sync.go',notebook)
overlay('internal/daemon/ws_outposts.go',wire)
overlay('internal/diag/diag.go',diag)
config=output/'overlay.json';config.write_text(json.dumps({'Replace':replace}))
subprocess.run(['go','build','-overlay',str(config),'-o',str(output/'attn'),'./cmd/attn'],check=True)
(output/'build.json').write_text(json.dumps({'commit':subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip(),'notebook_source_sha256':hashlib.sha256((root/'internal/daemon/notebook_sync.go').read_bytes()).hexdigest(),'instrumentation':replace},indent=2)+'\n')
print(output/'attn')
