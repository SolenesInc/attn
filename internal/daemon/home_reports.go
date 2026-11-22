package daemon

import (
	"errors"
	"strings"
	"sync"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/stepclock"
)

const ClaudeModReportRetryTimer stepclock.Name = "claude-mod-report-retry"

type homeReportQueue struct {
	mu      sync.Mutex
	queued  []homeReport
	sending bool
}

type homeReport struct {
	msg  protocol.Placed
	done func()
}

func (d *Daemon) enqueueHomeReport(msg protocol.Placed, done func()) {
	q := &d.homeReports
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queued = append(q.queued, homeReport{msg: msg, done: done})
	if !q.sending {
		q.sending = d.life.Go("homeReports", d.sendHomeReports)
	}
}

func (d *Daemon) sendHomeReports() {
	q := &d.homeReports
	for {
		q.mu.Lock()
		if len(q.queued) == 0 {
			q.sending = false
			q.mu.Unlock()
			return
		}
		next := q.queued[0]
		q.mu.Unlock()
		_, err := callHome[struct{}](d, next.msg)
		if errors.Is(err, errDaemonStopping) {
			return
		}
		if err != nil {
			d.logf("home report: the home refused %s; dropped it: %v", next.msg.CommandName(), err)
		}
		q.mu.Lock()
		q.queued = q.queued[1:]
		q.mu.Unlock()
		if next.done != nil {
			next.done()
		}
	}
}

func callHome[R any](d *Daemon, msg protocol.Placed) (R, error) {
	unreachable := false
	for {
		answer, err := Call[R](d.life.Context(), d.router(), msg)
		if err == nil || d.homeAnswered(err) {
			return answer, err
		}
		if !unreachable {
			d.logf("%s waits for the home, trying every %s: %v", msg.CommandName(), bridge.ReconnectInterval, err)
		}
		unreachable = true
		retry := d.clock.NewTimer(ClaudeModReportRetryTimer, bridge.ReconnectInterval)
		select {
		case <-retry.C():
		case <-d.life.Done():
			retry.Stop()
			return answer, errDaemonStopping
		}
	}
}

func (d *Daemon) homeAnswered(err error) bool {
	return d.enrollmentStatus().IsHome() || bridgeAnswered(err)
}

func bridgeAnswered(err error) bool {
	var rejected *bridge.CallRejected
	if !errors.As(err, &rejected) {
		return false
	}
	return !strings.Contains(rejected.Reason, errOutpostBridgeReplaced.Error()) && !strings.Contains(rejected.Reason, "daemon is stopping")
}
