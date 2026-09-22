package daemon

import (
	"context"
	"errors"
	"sync"

	"github.com/victorarias/attn/internal/protocol"
)

const productionSessionReopenWorkers = 4

const reopenGenerationChangedMessage = "the session changed after this row was listed; reload to see it"

func (d *Daemon) sessionReopenBroker() *sessionReopenBroker {
	d.reopenBrokerMu.Lock()
	defer d.reopenBrokerMu.Unlock()
	if d.reopenBrokerStopped {
		return nil
	}
	if d.reopenBrokerInstance == nil {
		d.reopenBrokerInstance = newSessionReopenBroker(d, productionSessionReopenWorkers)
	}
	return d.reopenBrokerInstance
}

func (d *Daemon) closeSessionReopenBroker() {
	d.reopenBrokerMu.Lock()
	d.reopenBrokerStopped = true
	broker := d.reopenBrokerInstance
	d.reopenBrokerInstance = nil
	d.reopenBrokerMu.Unlock()
	if broker != nil {
		broker.Stop()
	}
}

func (d *Daemon) removeSessionReopenClient(client *wsClient) {
	d.reopenBrokerMu.Lock()
	broker := d.reopenBrokerInstance
	d.reopenBrokerMu.Unlock()
	if broker != nil {
		broker.RemoveClient(client)
	}
}

type reopenPageIntent struct {
	Client *wsClient
	Epoch  uint64
	Append bool
}

type reopenBrokerClient struct {
	epoch uint64
	keys  map[reopenKey]struct{}
}

type reopenBrokerJob struct {
	key                   reopenKey
	ctx                   context.Context
	cancel                context.CancelFunc
	interests             map[*wsClient]struct{}
	broadcastOnCompletion bool
	started               bool
}

type sessionReopenBroker struct {
	daemon   *Daemon
	ctx      context.Context
	cancel   context.CancelFunc
	workers  int
	resolve  func(context.Context, reopenKey) (sessionReopenVerdict, error)
	wake     chan struct{}
	stopOnce sync.Once
	mu       sync.Mutex
	stopped  bool
	clients  map[*wsClient]*reopenBrokerClient
	jobs     map[reopenKey]*reopenBrokerJob
	queue    []*reopenBrokerJob
	workerWg sync.WaitGroup
}

func newSessionReopenBroker(daemon *Daemon, workers int) *sessionReopenBroker {
	if workers < 1 {
		workers = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &sessionReopenBroker{
		daemon:  daemon,
		ctx:     ctx,
		cancel:  cancel,
		workers: workers,
		wake:    make(chan struct{}, 1),
		clients: make(map[*wsClient]*reopenBrokerClient),
		jobs:    make(map[reopenKey]*reopenBrokerJob),
	}
	b.resolve = func(ctx context.Context, key reopenKey) (sessionReopenVerdict, error) {
		return (sessionReopenResolver{daemon: daemon}).ResolveClosed(ctx, key, daemon.scheduledReopenGit(gitDeferred))
	}
	for range workers {
		b.workerWg.Add(1)
		go b.work()
	}
	return b
}

func (b *sessionReopenBroker) BeginPage(client *wsClient, appendPage bool) reopenPageIntent {
	b.mu.Lock()
	defer b.mu.Unlock()
	if client.removedFromReopenBroker {
		return reopenPageIntent{Client: client}
	}
	state := b.clients[client]
	if state == nil {
		state = &reopenBrokerClient{keys: make(map[reopenKey]struct{})}
		b.clients[client] = state
	}
	if !appendPage {
		state.epoch++
		b.detachClientKeysLocked(client, state)
	} else if state.epoch == 0 {
		state.epoch = 1
	}
	return reopenPageIntent{Client: client, Epoch: state.epoch, Append: appendPage}
}

func (b *sessionReopenBroker) CommitPage(intent reopenPageIntent, keys []reopenKey) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return
	}
	state := b.clients[intent.Client]
	if state == nil || state.epoch != intent.Epoch {
		return
	}
	for _, key := range keys {
		if key.SessionID == "" || key.ClosedAt == "" {
			continue
		}
		if _, exists := state.keys[key]; exists {
			continue
		}
		state.keys[key] = struct{}{}
		job := b.jobLocked(key)
		job.interests[intent.Client] = struct{}{}
	}
}

func (b *sessionReopenBroker) ResolveForClose(key reopenKey) {
	if key.SessionID == "" || key.ClosedAt == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return
	}
	b.jobLocked(key).broadcastOnCompletion = true
}

func (b *sessionReopenBroker) RemoveClient(client *wsClient) {
	b.mu.Lock()
	defer b.mu.Unlock()
	client.removedFromReopenBroker = true
	state := b.clients[client]
	if state == nil {
		return
	}
	b.detachClientKeysLocked(client, state)
	delete(b.clients, client)
}

func (b *sessionReopenBroker) Stop() {
	b.stopOnce.Do(func() {
		b.mu.Lock()
		b.stopped = true
		for _, job := range b.jobs {
			job.cancel()
		}
		b.jobs = make(map[reopenKey]*reopenBrokerJob)
		b.queue = nil
		b.clients = make(map[*wsClient]*reopenBrokerClient)
		b.mu.Unlock()
		b.cancel()
		b.signal()
		b.workerWg.Wait()
	})
}

func (b *sessionReopenBroker) jobLocked(key reopenKey) *reopenBrokerJob {
	if job := b.jobs[key]; job != nil {
		return job
	}
	ctx, cancel := context.WithCancel(b.ctx)
	job := &reopenBrokerJob{
		key:       key,
		ctx:       ctx,
		cancel:    cancel,
		interests: make(map[*wsClient]struct{}),
	}
	b.jobs[key] = job
	b.queue = append(b.queue, job)
	b.signal()
	return job
}

func (b *sessionReopenBroker) detachClientKeysLocked(client *wsClient, state *reopenBrokerClient) {
	for key := range state.keys {
		job := b.jobs[key]
		if job == nil {
			continue
		}
		delete(job.interests, client)
		if len(job.interests) == 0 && !job.broadcastOnCompletion {
			delete(b.jobs, key)
			job.cancel()
		}
	}
	state.keys = make(map[reopenKey]struct{})
}

func (b *sessionReopenBroker) signal() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func (b *sessionReopenBroker) work() {
	defer b.workerWg.Done()
	for {
		job := b.nextJob()
		if job == nil {
			select {
			case <-b.ctx.Done():
				return
			case <-b.wake:
				continue
			}
		}
		verdict, err := b.resolve(job.ctx, job.key)
		b.finish(job, verdict, err)
	}
}

func (b *sessionReopenBroker) nextJob() *reopenBrokerJob {
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(b.queue) > 0 {
		job := b.queue[0]
		b.queue = b.queue[1:]
		if b.jobs[job.key] != job || job.ctx.Err() != nil {
			continue
		}
		job.started = true
		return job
	}
	return nil
}

func (b *sessionReopenBroker) finish(job *reopenBrokerJob, verdict sessionReopenVerdict, resolveErr error) {
	b.mu.Lock()
	if b.jobs[job.key] != job {
		b.mu.Unlock()
		return
	}
	delete(b.jobs, job.key)
	clients := make([]*wsClient, 0, len(job.interests))
	for client := range job.interests {
		clients = append(clients, client)
		if state := b.clients[client]; state != nil {
			delete(state.keys, job.key)
		}
	}
	broadcast := job.broadcastOnCompletion
	b.mu.Unlock()
	job.cancel()

	if errors.Is(resolveErr, context.Canceled) {
		return
	}
	message := protocol.SessionReopenResolvedMessage{
		Event:     protocol.EventSessionReopenResolved,
		SessionID: job.key.SessionID,
		ClosedAt:  job.key.ClosedAt,
		Success:   resolveErr == nil,
	}
	if errors.Is(resolveErr, errStaleReopenGeneration) {
		message.Error = protocol.Ptr(reopenGenerationChangedMessage)
	} else if resolveErr != nil {
		message.Error = protocol.Ptr(resolveErr.Error())
	} else {
		message.Reopen = verdict.toProtocol()
		b.daemon.publishFact(FactSessionReopenRefreshed, job.key.SessionID, message.Reopen)
	}
	if broadcast {
		b.daemon.broadcastMessage(&message)
		return
	}
	for _, client := range clients {
		b.daemon.sendToClient(client, &message)
	}
}
