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

type attentionLoggedFacts struct {
	Stake *struct {
		ID string `json:"id"`
	} `json:"stake"`
	Tended []struct {
		ID string `json:"id"`
	} `json:"tended"`
	Wakes []struct {
		Seed string `json:"seed"`
	} `json:"wakes"`
	Delegates []struct {
		Session protocol.SessionID `json:"session"`
		Stake   string             `json:"stake"`
	} `json:"delegates"`
}

func (f attentionLoggedFacts) subjects() []string {
	facts := attention.Facts{}
	if f.Stake != nil {
		facts.Stake = &attention.Seed{ID: f.Stake.ID}
	}
	for _, seed := range f.Tended {
		facts.Tended = append(facts.Tended, attention.Seed{ID: seed.ID})
	}
	for _, wake := range f.Wakes {
		facts.Wakes = append(facts.Wakes, attention.Wake{Seed: wake.Seed})
	}
	for _, delegate := range f.Delegates {
		facts.Delegates = append(facts.Delegates, attention.Delegate{Session: delegate.Session, Stake: delegate.Stake})
	}
	return attentionSubjects(facts)
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
			var facts attentionLoggedFacts
			_ = json.Unmarshal(entry.Facts, &facts)
			judged[entry.Session] = judgedFacts{key: entry.Key, subjects: facts.subjects()}
			pending[verdictID{entry.Session, entry.At.UTC()}] = true
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
	d.attention.mu.Unlock()
	for verdict := range pending {
		d.appendAttentionLog(attention.LogEntry{Kind: attention.LogFollowUp, At: d.clock.Now(), Session: verdict.session, VerdictAt: &verdict.at, FollowUp: followUpUnknown})
	}
}
