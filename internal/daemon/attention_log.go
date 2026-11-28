package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	// A verdict line holds the facts and at most 4,000 characters of the last message; 1 MiB is a tripwire
	// past any real line.
	attentionLogLineLimit = 1 << 20
	// About 65 verdicts a day (F16 on s-4kr3bn). The shadow week measures the real size; past 256 MiB the log
	// moves to attention-decisions.jsonl.1 and starts again.
	attentionLogTripwire = 256 << 20
)

func (d *Daemon) attentionLogPath() string {
	return filepath.Join(d.dataRoot, attention.DecisionLogFile)
}

func (d *Daemon) appendAttentionLog(entry attention.LogEntry) {
	line, err := json.Marshal(entry)
	if err == nil {
		var file *os.File
		file, err = os.OpenFile(d.attentionLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			_, err = file.Write(append(line, '\n'))
			err = errors.Join(err, file.Close())
		}
	}
	if err != nil {
		d.logf("attention: decision log: %v", err)
	}
}

func (d *Daemon) loadAttentionLog() {
	path := d.attentionLogPath()
	if info, err := os.Stat(path); err == nil && info.Size() > attentionLogTripwire {
		d.logf("attention: decision log reached %d bytes, past its %d byte tripwire; moving it to %s.1", info.Size(), attentionLogTripwire, path)
		if err := os.Rename(path, path+".1"); err != nil {
			d.logf("attention: decision log: %v", err)
		}
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		d.logf("attention: decision log: %v", err)
		return
	}
	defer file.Close()
	judged := map[protocol.SessionID]judgedFacts{}
	surfaced := map[protocol.SessionID]attention.LogEntry{}
	waits := map[protocol.SessionID]*supervisedWait{}
	type verdictID struct {
		session protocol.SessionID
		at      time.Time
	}
	pending := map[verdictID]bool{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), attentionLogLineLimit)
	for scanner.Scan() {
		var entry attention.LogEntry
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		switch entry.Kind {
		case attention.LogVerdict:
			judged[entry.Session] = judgedFrom(entry)
			pending[verdictID{entry.Session, entry.At.UTC()}] = true
		case attention.LogSurfaced:
			if entry.VerdictAt != nil && d.store.Get(entry.Session) != nil {
				surfaced[entry.Session] = entry
			}
		case attention.LogNotice:
			if entry.To == entry.Session && d.store.Get(entry.Session) != nil {
				waits[entry.Session] = &supervisedWait{since: entry.WaitSince, notice: entry.Checkpoint, noticedAt: entry.At, silent: entry.SilentSince}
			}
		case attention.LogWaitEnded:
			delete(waits, entry.Session)
		case attention.LogFollowUp:
			if entry.VerdictAt != nil {
				delete(pending, verdictID{entry.Session, entry.VerdictAt.UTC()})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		d.logf("attention: decision log: %v", err)
	}
	d.attention.mu.Lock()
	d.attention.judged = judged
	d.attention.waits = waits
	d.attention.mu.Unlock()
	for _, entry := range surfaced {
		d.attention.surface(entry)
		d.broadcastSessionStateChanged(entry.Session)
	}
	for verdict := range pending {
		d.appendAttentionLog(attention.LogEntry{Kind: attention.LogFollowUp, At: d.clock.Now(), Session: verdict.session, VerdictAt: &verdict.at, FollowUp: followUpUnknown})
	}
}
