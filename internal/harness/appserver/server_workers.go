package appserver

import (
	"context"

	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

type inFlightAdoption struct {
	done        chan struct{}
	reopen      bool
	retryReopen bool
}

func (p *serverConnection) resetAdoptionCache() {
	p.mu.Lock()
	p.sessionGen++
	p.externalGen = nil
	p.declinedIdle = nil
	p.mu.Unlock()
}

func (p *serverConnection) bumpSessionGen() {
	p.mu.Lock()
	p.sessionGen++
	p.externalGen = nil
	p.mu.Unlock()
}

func (p *serverConnection) currentSessionGen() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessionGen
}

func (p *serverConnection) markNonWorker(conversation string) {
	p.mu.Lock()
	if p.nonWorkers == nil {
		p.nonWorkers = make(map[string]bool)
	}
	p.nonWorkers[conversation] = true
	p.mu.Unlock()
}

func (p *serverConnection) markExternal(conversation string, gen uint64) {
	p.mu.Lock()
	if p.sessionGen == gen {
		if p.externalGen == nil {
			p.externalGen = make(map[string]uint64)
		}
		p.externalGen[conversation] = gen
	}
	p.mu.Unlock()
}

func (p *serverConnection) markDeclinedIdle(conversation string) {
	p.mu.Lock()
	if p.declinedIdle == nil {
		p.declinedIdle = make(map[string]bool)
	}
	p.declinedIdle[conversation] = true
	p.mu.Unlock()
}

func (p *serverConnection) clearDeclinedIdle(conversation string) {
	p.mu.Lock()
	delete(p.declinedIdle, conversation)
	p.mu.Unlock()
}

func (p *serverConnection) isDeclinedIdle(conversation string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.declinedIdle[conversation]
}

func (p *serverConnection) skipStatusAdoption(conversation string, reopen bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.nonWorkers[conversation] {
		return true
	}
	if p.sessionGen > 0 && p.externalGen[conversation] == p.sessionGen {
		return true
	}
	if reopen {
		delete(p.declinedIdle, conversation)
		return false
	}
	return p.declinedIdle[conversation]
}

func (p *serverConnection) claimAdoption(conversation string, reopen bool) (*inFlightAdoption, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing := p.adopting[conversation]; existing != nil {
		if reopen && !existing.reopen {
			existing.retryReopen = true
		}
		return existing, false
	}
	if p.adopting == nil {
		p.adopting = make(map[string]*inFlightAdoption)
	}
	flight := &inFlightAdoption{done: make(chan struct{}), reopen: reopen}
	p.adopting[conversation] = flight
	return flight, true
}

func (p *serverConnection) finishAdoption(conversation string, flight *inFlightAdoption) bool {
	p.mu.Lock()
	retry := flight.retryReopen
	if p.adopting[conversation] == flight {
		delete(p.adopting, conversation)
	}
	p.mu.Unlock()
	close(flight.done)
	return retry
}

func (p *serverConnection) drainAdoption(ctx context.Context, conversation string) {
	if conversation == "" {
		return
	}
	for {
		p.mu.Lock()
		flight := p.adopting[conversation]
		if flight != nil {
			flight.retryReopen = false
		}
		p.mu.Unlock()
		if flight == nil {
			return
		}
		select {
		case <-flight.done:
		case <-ctx.Done():
			return
		}
	}
}

func (p *serverConnection) adoptFromStatus(c *effectrpc.Client, conversation string, reopen bool) {
	if conversation == "" || p.holder(conversation) != "" || p.skipStatusAdoption(conversation, reopen) {
		return
	}
	flight, owner := p.claimAdoption(conversation, reopen)
	if !owner {
		return
	}
	if !p.c.host.Life().Go("adoptWorker", func() {
		ctx := p.c.host.Life().Context()
		listed, err := p.listConversations(ctx, c)
		var retry bool
		if err == nil {
			byID := indexListedConversations(listed)
			retry = p.adoptOwned(ctx, conversation, byID, flight, reopen)
		} else {
			retry = p.finishAdoption(conversation, flight)
		}
		if retry && p.holder(conversation) == "" && p.client() == c {
			p.adoptFromStatus(c, conversation, true)
		}
	}) {
		p.finishAdoption(conversation, flight)
	}
}

func (p *serverConnection) adoptListed(c *effectrpc.Client, listed []listedConversation) {
	byID := indexListedConversations(listed)
	for _, each := range listed {
		if each.kind() != workerConversation || p.holder(each.ID) != "" {
			continue
		}
		reopen := p.busy[each.ID] != ""
		if !reopen && p.isDeclinedIdle(each.ID) {
			continue
		}
		if reopen {
			p.clearDeclinedIdle(each.ID)
		}
		if _, _, _, ok := p.adoptionChain(each.ID, byID); !ok {
			continue
		}
		p.adoptFromListed(c, each.ID, byID, reopen)
	}
}

func (p *serverConnection) adoptFromListed(c *effectrpc.Client, conversation string, byID map[string]listedConversation, reopen bool) {
	flight, owner := p.claimAdoption(conversation, reopen)
	if !owner {
		return
	}
	if !p.c.host.Life().Go("adoptWorker", func() {
		ctx := p.c.host.Life().Context()
		retry := p.adoptOwned(ctx, conversation, byID, flight, reopen)
		if retry && p.holder(conversation) == "" && p.client() == c {
			p.adoptFromListed(c, conversation, byID, true)
		}
	}) {
		p.finishAdoption(conversation, flight)
	}
}

func (p *serverConnection) adoptForCaller(conversation string) protocol.SessionID {
	if conversation == "" {
		return ""
	}
	if held := p.holder(conversation); held != "" {
		return held
	}
	c := p.client()
	if c == nil {
		return ""
	}
	p.clearDeclinedIdle(conversation)
	ctx, cancel := context.WithTimeout(p.c.host.Life().Context(), serverCallLimit)
	defer cancel()
	for {
		if held := p.holder(conversation); held != "" {
			return held
		}
		flight, owner := p.claimAdoption(conversation, true)
		if !owner {
			select {
			case <-flight.done:
			case <-ctx.Done():
				return p.holder(conversation)
			}
			if held := p.holder(conversation); held != "" || flight.reopen {
				return held
			}
			continue
		}
		listed, err := p.listConversations(ctx, c)
		if err != nil {
			p.finishAdoption(conversation, flight)
			return ""
		}
		p.adoptOwned(ctx, conversation, indexListedConversations(listed), flight, true)
		return p.holder(conversation)
	}
}

func (p *serverConnection) adoptOwned(ctx context.Context, conversation string, byID map[string]listedConversation, flight *inFlightAdoption, reopen bool) bool {
	parentSession, ancestors, target, ok := p.adoptionChain(conversation, byID)
	if !ok {
		return p.finishAdoption(conversation, flight)
	}
	for _, ancestor := range ancestors {
		parentSession = p.adoptAncestor(ctx, ancestor, parentSession, reopen)
		if parentSession == "" {
			return p.finishAdoption(conversation, flight)
		}
	}
	p.commitWorkerAdoption(ctx, target, parentSession, reopen)
	return p.finishAdoption(conversation, flight)
}

func (p *serverConnection) adoptAncestor(ctx context.Context, worker listedConversation, parent protocol.SessionID, reopen bool) protocol.SessionID {
	for {
		if held := p.holder(worker.ID); held != "" {
			return held
		}
		flight, owner := p.claimAdoption(worker.ID, reopen)
		if !owner {
			select {
			case <-flight.done:
			case <-ctx.Done():
				return p.holder(worker.ID)
			}
			if held := p.holder(worker.ID); held != "" || !reopen || flight.reopen {
				return held
			}
			continue
		}
		p.commitWorkerAdoption(ctx, worker, parent, reopen)
		retry := p.finishAdoption(worker.ID, flight)
		if held := p.holder(worker.ID); held != "" {
			return held
		}
		if retry && !reopen {
			reopen = true
			continue
		}
		return ""
	}
}

func (p *serverConnection) commitWorkerAdoption(ctx context.Context, worker listedConversation, parent protocol.SessionID, reopen bool) {
	if p.holder(worker.ID) != "" {
		return
	}
	wait := p.c.host.Events().Adopted(harness.Adoption{
		Parent:       parent,
		Conversation: worker.ID,
		Title:        worker.workerTitle(),
		Reopen:       reopen,
	})
	if wait != nil {
		if err := wait(ctx); err != nil {
			p.c.host.Logf("adopt worker conversation %s: %v", worker.ID, err)
			return
		}
	}
	if id, l := p.holderWithLink(worker.ID); id != "" && l != nil {
		p.clearDeclinedIdle(worker.ID)
		p.c.host.Life().Go("reconcileAdoptedWorker", func() {
			p.reconcile(l, []protocol.SessionID{id}, false)
		})
		return
	}
	if !reopen {
		p.markDeclinedIdle(worker.ID)
	}
}

func (p *serverConnection) adoptionChain(conversation string, byID map[string]listedConversation) (protocol.SessionID, []listedConversation, listedConversation, bool) {
	gen := p.currentSessionGen()
	info, exists := byID[conversation]
	if !exists {
		return "", nil, listedConversation{}, false
	}
	if info.kind() != workerConversation {
		p.markNonWorker(conversation)
		return "", nil, listedConversation{}, false
	}
	if p.holder(conversation) != "" {
		return "", nil, listedConversation{}, false
	}
	visited := map[string]bool{conversation: true}
	var reversed []listedConversation
	parentConv := info.Lineage.Parent
	for parentConv != "" {
		if parentSession := p.holder(parentConv); parentSession != "" {
			ancestors := make([]listedConversation, len(reversed))
			for i := range reversed {
				ancestors[i] = reversed[len(reversed)-1-i]
			}
			return parentSession, ancestors, info, true
		}
		if visited[parentConv] {
			p.markExternal(conversation, gen)
			return "", nil, listedConversation{}, false
		}
		visited[parentConv] = true
		parentInfo, ok := byID[parentConv]
		if !ok {
			p.markExternal(conversation, gen)
			return "", nil, listedConversation{}, false
		}
		switch parentInfo.kind() {
		case workerConversation:
			reversed = append(reversed, parentInfo)
		case hostedSubagentConversation:
		default:
			p.markExternal(conversation, gen)
			return "", nil, listedConversation{}, false
		}
		parentConv = parentInfo.Lineage.Parent
	}
	p.markExternal(conversation, gen)
	return "", nil, listedConversation{}, false
}

func indexListedConversations(listed []listedConversation) map[string]listedConversation {
	byID := make(map[string]listedConversation, len(listed))
	for _, each := range listed {
		byID[each.ID] = each
	}
	return byID
}
