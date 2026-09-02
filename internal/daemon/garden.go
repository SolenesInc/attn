package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// Measured 2026-08-12 against production ~/.attn: the largest whole list attn
// pushes is 59 tickets, so docstore.MaxLimit is a tripwire, not a budget.
const gardenSnapshotLimit = docstore.MaxLimit

func (d *Daemon) ensureGardenCollections() {
	if d.store == nil {
		return
	}
	for _, schema := range []docstore.CollectionSchema{
		garden.SeedsSchema(),
		garden.NotesSchema(),
		garden.DispatchesSchema(),
		garden.ReviewRunsSchema(),
		garden.ReviewItemsSchema(),
	} {
		redeclared, err := d.store.DefineDocumentCollection(schema, time.Now())
		if err != nil {
			d.logf("garden: declaring %s/%s: %v", schema.Namespace, schema.Collection, err)
			continue
		}
		if redeclared {
			d.publishCollectionRedeclared(schema.Namespace, schema.Collection)
		}
	}
	d.dispatchSeedsMu.Lock()
	d.dispatchSeeds, d.dispatchFromChief, d.dispatchProjectionRevs, d.dispatchSeedsLoaded = nil, nil, nil, false
	d.dispatchSeedsMu.Unlock()
}

func (d *Daemon) seedsCollection() (*docstore.CollectionSchema, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	return d.collectionFor(garden.Namespace, garden.CollectionSeeds)
}

func (d *Daemon) plantSeed(schema docstore.CollectionSchema, seed garden.Seed) (docstore.Document, error) {
	seed = d.initializeSeedLifecycle(seed)
	body, err := seed.Encode()
	if err != nil {
		return docstore.Document{}, err
	}
	expected := docstore.ExpectAbsent
	fact := documentChangedFact(garden.Namespace, garden.CollectionSeeds, seed.ID, false)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: schema, ID: seed.ID, Body: body, Expected: &expected,
	}, fact, d.gardenTime())
	if err != nil {
		return docstore.Document{}, err
	}
	d.announceCommittedWrite(fact, written.Seq)
	d.publishFact(FactGardenPlanted, seed.ID, nil)

	doc, found, err := d.store.GetDocument(schema, seed.ID)
	if err != nil || !found {
		return docstore.Document{ID: seed.ID, Body: body, Rev: written.Rev}, nil
	}
	return *doc, nil
}

func (d *Daemon) writeSeed(schema docstore.CollectionSchema, seed garden.Seed, expected int64, fact string) (docstore.Document, error) {
	body, err := seed.Encode()
	if err != nil {
		return docstore.Document{}, err
	}
	changed := documentChangedFact(garden.Namespace, garden.CollectionSeeds, seed.ID, false)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: schema, ID: seed.ID, Body: body, Expected: &expected,
	}, changed, d.gardenTime())
	if err != nil {
		return docstore.Document{}, err
	}
	d.announceCommittedWrite(changed, written.Seq)
	d.publishFact(fact, seed.ID, nil)

	doc, found, err := d.store.GetDocument(schema, seed.ID)
	if err != nil || !found {
		return docstore.Document{ID: seed.ID, Body: body, Rev: written.Rev}, nil
	}
	return *doc, nil
}

func (d *Daemon) readSeed(id string) (garden.Seed, docstore.Document, error) {
	id = strings.TrimSpace(id)
	if err := garden.ValidateID(id); err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	doc, found, err := d.store.GetDocument(*schema, id)
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	if !found {
		return garden.Seed{}, docstore.Document{}, fmt.Errorf("no seed %s is planted here; `attn seed ls` lists the garden", id)
	}
	seed, err := garden.Decode(doc.Body)
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	return seed, *doc, nil
}

type gardenRead struct {
	seeds []garden.Seed
	docs  map[string]docstore.Document
	ready map[string]bool
}

func (d *Daemon) readGarden() (gardenRead, error) {
	read, _, err := d.runDocQuery(docstore.Query{
		Namespace:  garden.Namespace,
		Collection: garden.CollectionSeeds,
		Sort:       &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
		Limit:      gardenSnapshotLimit,
	})
	if err != nil {
		return gardenRead{}, err
	}
	out := gardenRead{
		seeds: make([]garden.Seed, 0, len(read.Documents)),
		docs:  make(map[string]docstore.Document, len(read.Documents)),
		ready: map[string]bool{},
	}
	for _, doc := range read.Documents {
		seed, err := garden.Decode(doc.Body)
		if err != nil {
			d.logf("garden: seed %s has an unreadable body: %v", doc.ID, err)
			continue
		}
		out.seeds = append(out.seeds, seed)
		out.docs[seed.ID] = doc
	}
	for _, seed := range garden.Ready(out.seeds, d.sessionExists) {
		out.ready[seed.ID] = true
	}
	return out, nil
}

func (g gardenRead) wire(seeds []garden.Seed) []protocol.Seed {
	out := make([]protocol.Seed, 0, len(seeds))
	for _, seed := range seeds {
		wire := seedToProtocol(seed, g.docs[seed.ID], g.ready[seed.ID])
		if progress, ok := g.progress(seed.ID); ok {
			wire.PlotProgress = progress
		}
		out = append(out, wire)
	}
	return out
}

func (g gardenRead) progress(id string) (*protocol.SeedPlotProgress, bool) {
	p := garden.PlotProgress(g.seeds, id, g.ready)
	if p.Total == 0 {
		return nil, false
	}
	return &protocol.SeedPlotProgress{
		Total:    p.Total,
		Done:     p.Done,
		Withered: p.Withered,
		Growing:  p.Growing,
		Dormant:  p.Dormant,
		Ready:    p.Ready,
		Blocked:  p.Blocked,
	}, true
}

func (d *Daemon) gardenReady() map[string]bool {
	read, err := d.readGarden()
	if err != nil {
		d.logf("garden: reading the garden to decide readiness: %v", err)
		return map[string]bool{}
	}
	return read.ready
}

func (d *Daemon) countSeeds() int {
	if d.store == nil {
		return 0
	}
	read, found, err := d.store.CountQuery(docstore.Query{
		Namespace: garden.Namespace, Collection: garden.CollectionSeeds,
	})
	if err != nil || !found {
		return 0
	}
	return read.Count
}

func seedToProtocol(seed garden.Seed, doc docstore.Document, ready bool) protocol.Seed {
	stateChangedAt := strings.TrimSpace(seed.StateChangedAt)
	stateChangedAtExact := stateChangedAt != ""
	if stateChangedAt == "" {
		stateChangedAt = doc.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	out := protocol.Seed{
		ID:                  seed.ID,
		Title:               seed.Title,
		Body:                seed.Body,
		Status:              seed.Status,
		StepSlug:            seed.StepSlug,
		PlanterSession:      seed.PlanterSession,
		PlanterMember:       seed.PlanterMember,
		TenderSession:       seed.TenderSession,
		TenderMember:        seed.TenderMember,
		LastExecutionID:     protocol.Ptr(strings.TrimSpace(seed.LastExecutionID)),
		StateChangedAt:      stateChangedAt,
		StateChangedAtExact: stateChangedAtExact,
		Edges:               make([]protocol.SeedEdge, 0, len(seed.Edges)),
		Template:            seed.Template,
		Gate:                seed.Gate,
		Ready:               ready,
		Vars:                make([]protocol.SeedVar, 0, len(seed.Vars)),
		Rev:                 int(doc.Rev),
		CreatedAt:           doc.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:           doc.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if strings.TrimSpace(seed.LastExecutionID) == "" {
		out.LastExecutionID = nil
	}
	if seed.Reason != "" {
		out.Reason = protocol.Ptr(seed.Reason)
	}
	if seed.ResumeSessionID != "" {
		out.ResumeSessionID = protocol.Ptr(seed.ResumeSessionID)
		out.ResumeCwd = protocol.Ptr(seed.ResumeCwd)
		out.ResumeAgent = protocol.Ptr(seed.ResumeAgent)
	}
	if seed.HarvestWhen != nil {
		condition := protocol.SeedHarvestCondition{
			PullRequest: seed.HarvestWhen.PullRequest,
			URL:         seed.HarvestWhen.URL,
			SetAt:       seed.HarvestWhen.SetAt,
		}
		if seed.HarvestWhen.SetBySession != "" {
			condition.SetBySession = protocol.Ptr(seed.HarvestWhen.SetBySession)
		}
		if seed.HarvestWhen.SetByMember != "" {
			condition.SetByMember = protocol.Ptr(seed.HarvestWhen.SetByMember)
		}
		out.HarvestWhen = &condition
	}
	for _, e := range seed.Edges {
		out.Edges = append(out.Edges, protocol.SeedEdge{Kind: e.Kind, To: e.To})
	}
	for _, v := range seed.Vars {
		wire := protocol.SeedVar{Name: v.Name}
		if v.Description != "" {
			wire.Description = protocol.Ptr(v.Description)
		}
		if v.Required {
			wire.Required = protocol.Ptr(true)
		}
		if v.Default != "" {
			wire.Default = protocol.Ptr(v.Default)
		}
		if v.Pattern != "" {
			wire.Pattern = protocol.Ptr(v.Pattern)
		}
		if len(v.Enum) > 0 {
			wire.Enum = v.Enum
		}
		out.Vars = append(out.Vars, wire)
	}
	return out
}

func (d *Daemon) seedsForBroadcast() []protocol.Seed {
	if d.store == nil {
		return nil
	}
	if err := d.requireHome(garden.Surface); err != nil {
		return nil
	}
	read, err := d.readGarden()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("garden: reading seeds for broadcast: %v", err)
		}
		return nil
	}
	return read.wire(read.seeds)
}

func (d *Daemon) countSeedsForBroadcast() int {
	if d.store == nil {
		return 0
	}
	if err := d.requireHome(garden.Surface); err != nil {
		return 0
	}
	return d.countSeeds()
}

func (d *Daemon) projectGardenSeeds() {
	if d.store == nil {
		return
	}
	d.projectSnapshot(snapshotGarden, func() {
		seeds := d.seedsForBroadcast()
		total := d.countSeedsForBroadcast()
		if total > len(seeds) {
			d.logf("garden: %d seeds, pushing the newest %d (limit %d); the panel says so",
				total, len(seeds), gardenSnapshotLimit)
		}
		if d.gardenBroadcastHook != nil {
			d.gardenBroadcastHook(seeds, total)
		}
		if d.wsHub == nil {
			return
		}
		d.broadcastMessage(&protocol.GardenSeedsUpdatedMessage{
			Event: protocol.EventGardenSeedsUpdated,
			Seeds: seeds,
			Total: total,
		})
	})
}

func (d *Daemon) sendGardenError(conn net.Conn, verb string, err error) {
	d.sendError(conn, fmt.Sprintf("seed %s: %v", verb, err))
}

func (d *Daemon) handleSeedPlant(conn net.Conn, msg *protocol.SeedPlantMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "plant", err)
		return
	}
	title := strings.TrimSpace(msg.Title)
	body := protocol.Deref(msg.Body)
	if err := garden.ValidatePlant(title, body); err != nil {
		d.sendGardenError(conn, "plant", err)
		return
	}
	schema, err := d.seedsCollection()
	if err != nil {
		d.sendGardenError(conn, "plant", err)
		return
	}

	sessionID := strings.TrimSpace(protocol.Deref(msg.SourceSessionID))
	seed := garden.Seed{
		Title:          title,
		Body:           body,
		Status:         garden.StatusPlanted,
		StepSlug:       garden.StepSlug(title),
		PlanterSession: sessionID,
		PlanterMember:  d.resolveTenderMember(protocol.Deref(msg.Member), sessionID),
		Edges:          []garden.Edge{},
		Vars:           []garden.Var{},
	}
	resumeID, resumeCwd, resumeAgent, err := normalizeSeedResumeIdentity(
		protocol.Deref(msg.ResumeSessionID), protocol.Deref(msg.ResumeCwd), protocol.Deref(msg.ResumeAgent),
	)
	if err != nil {
		d.sendGardenError(conn, "plant", err)
		return
	}
	seed.ResumeSessionID, seed.ResumeCwd, seed.ResumeAgent = resumeID, resumeCwd, resumeAgent
	if plot := strings.TrimSpace(protocol.Deref(msg.PartOf)); plot != "" {
		if _, _, err := d.readSeed(plot); err != nil {
			d.sendGardenError(conn, "plant", err)
			return
		}
		seed.Edges = append(seed.Edges, garden.Edge{Kind: garden.EdgePartOf, To: plot})
	}
	if origin := strings.TrimSpace(protocol.Deref(msg.DiscoveredFrom)); origin != "" {
		if _, _, err := d.readSeed(origin); err != nil {
			d.sendGardenError(conn, "plant", err)
			return
		}
		seed.Edges = append(seed.Edges, garden.Edge{Kind: garden.EdgeDiscoveredFrom, To: origin})
	}
	seed, doc, err := d.mintAndPlant(*schema, seed)
	if err != nil {
		d.sendGardenError(conn, "plant", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{
		Ok:              true,
		SeedPlantResult: &protocol.SeedPlantResult{Seed: seedToProtocol(seed, doc, d.gardenReady()[seed.ID])},
	})
}

func (d *Daemon) handleSeedPlot(conn net.Conn, msg *protocol.SeedPlotMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "plot", err)
		return
	}
	spec := garden.PlotSpec{Title: strings.TrimSpace(msg.Title), Body: protocol.Deref(msg.Body)}
	for _, child := range msg.Children {
		spec.Children = append(spec.Children, garden.PlotChildSpec{
			Title:  strings.TrimSpace(child.Title),
			Body:   protocol.Deref(child.Body),
			Blocks: child.Blocks,
		})
	}
	if err := garden.ValidatePlotSpec(spec); err != nil {
		d.sendGardenError(conn, "plot", err)
		return
	}
	schema, err := d.seedsCollection()
	if err != nil {
		d.sendGardenError(conn, "plot", err)
		return
	}
	sessionID := strings.TrimSpace(protocol.Deref(msg.SourceSessionID))
	member := strings.TrimSpace(protocol.Deref(msg.Member))

	var result protocol.SeedPlotResult
	planted := []string{}

	var plotErr error
	d.coalesceSnapshots(func() {
		crown, crownDoc, err := d.mintAndPlant(*schema, garden.Seed{
			Title: spec.Title, Body: spec.Body, Status: garden.StatusPlanted,
			StepSlug: garden.StepSlug(spec.Title), PlanterSession: sessionID, PlanterMember: member,
			Edges: []garden.Edge{}, Vars: []garden.Var{},
		})
		if err != nil {
			plotErr = err
			return
		}
		planted = append(planted, crown.ID)
		ids := make(map[string]string, len(spec.Children))
		childSeeds := make([]garden.Seed, 0, len(spec.Children))
		for _, child := range spec.Children {
			id, err := d.mintUnplantedSeedID(*schema)
			if err != nil {
				plotErr = err
				return
			}
			ids[garden.StepSlug(child.Title)] = id
			childSeeds = append(childSeeds, garden.Seed{ID: id, Title: child.Title, Body: child.Body})
		}
		docs := make([]docstore.Document, 0, len(spec.Children))
		for i, child := range spec.Children {
			seed := childSeeds[i]
			seed.Edges = []garden.Edge{{Kind: garden.EdgePartOf, To: crown.ID}}
			for _, target := range child.Blocks {
				seed.Edges = append(seed.Edges, garden.Edge{Kind: garden.EdgeBlocks, To: ids[garden.StepSlug(strings.TrimSpace(target))]})
			}
			seed.Status = garden.StatusPlanted
			seed.StepSlug = garden.StepSlug(seed.Title)
			seed.PlanterSession = sessionID
			seed.PlanterMember = member
			seed.Vars = []garden.Var{}
			doc, err := d.plantSeed(*schema, seed)
			if err != nil {
				plotErr = fmt.Errorf(
					"planting %q failed after %s were planted: %w — the plot is partial, `attn seed ls` shows what landed",
					seed.Title, strings.Join(planted, ", "), err)
				return
			}
			planted = append(planted, seed.ID)
			childSeeds[i] = seed
			docs = append(docs, doc)
		}
		ready := d.gardenReady()
		result.Crown = seedToProtocol(crown, crownDoc, ready[crown.ID])
		for i, seed := range childSeeds {
			result.Children = append(result.Children, seedToProtocol(seed, docs[i], ready[seed.ID]))
		}
	})
	if plotErr != nil {
		d.sendGardenError(conn, "plot", plotErr)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedPlotResult: &result})
}

// mintAttempts receipt: at 10k seeds one crypto/rand mint collides with p~1e-5, so three in a row is a broken random source.
func (d *Daemon) mintAndPlant(schema docstore.CollectionSchema, seed garden.Seed) (garden.Seed, docstore.Document, error) {
	seed = d.initializeSeedLifecycle(seed)
	const mintAttempts = 3
	var lastErr error
	for range mintAttempts {
		id, err := d.mintSeedID()
		if err != nil {
			return seed, docstore.Document{}, err
		}
		seed.ID = id
		doc, err := d.plantSeed(schema, seed)
		if err == nil {
			return seed, doc, nil
		}
		if !docstore.IsConflict(err) {
			return seed, docstore.Document{}, err
		}
		lastErr = err
	}
	return seed, docstore.Document{}, fmt.Errorf(
		"minted %d seed ids and every one was already planted, which a working random source does not do: %w",
		mintAttempts, lastErr)
}

func (d *Daemon) mintSeedID() (string, error) {
	if d.gardenMintID != nil {
		return d.gardenMintID()
	}
	return garden.NewID()
}

func (d *Daemon) mintUnplantedSeedID(schema docstore.CollectionSchema) (string, error) {
	// Tripwire: at ten thousand seeds a single mint collides with probability
	// ~1e-5, so three in a row is a broken random source.
	const mintAttempts = 3
	for range mintAttempts {
		id, err := d.mintSeedID()
		if err != nil {
			return "", err
		}
		_, found, err := d.store.GetDocument(schema, id)
		if err != nil {
			return "", err
		}
		if !found {
			return id, nil
		}
	}
	return "", fmt.Errorf("minted %d seed ids and every one was already planted, which a working random source does not do", mintAttempts)
}

func (d *Daemon) handleSeedList(conn net.Conn, msg *protocol.SeedListMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "ls", err)
		return
	}
	read, err := d.readGarden()
	if err != nil {
		d.sendGardenError(conn, "ls", err)
		return
	}
	result := &protocol.SeedListResult{Total: d.countSeeds()}
	seeds := read.seeds
	if protocol.Deref(msg.Stale) {
		window := garden.DefaultStaleWindow
		if s := protocol.Deref(msg.StaleWindowSeconds); s > 0 {
			window = time.Duration(s) * time.Second
		}
		seeds, err = d.staleSeeds(read, window)
		if err != nil {
			d.sendGardenError(conn, "ls", err)
			return
		}
		result.Total = len(seeds)
		result.StaleWindowSeconds = protocol.Ptr(int(window / time.Second))
	}
	result.Seeds = read.wire(seeds)
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedListResult: result})
}

func (d *Daemon) staleSeeds(read gardenRead, window time.Duration) ([]garden.Seed, error) {
	now := time.Now()
	lastMoved := make(map[string]time.Time, len(read.seeds))
	for _, seed := range read.seeds {
		if garden.Closed(seed.Status) {
			continue
		}
		moved := read.docs[seed.ID].UpdatedAt
		if now.Sub(moved) >= window {
			note, err := d.newestNoteAt(seed.ID)
			if err != nil {
				return nil, err
			}
			if note.After(moved) {
				moved = note
			}
		}
		lastMoved[seed.ID] = moved
	}
	return garden.Stale(read.seeds, lastMoved, window, now), nil
}

func (d *Daemon) newestNoteAt(seedID string) (time.Time, error) {
	readNotes, _, err := d.runDocQuery(docstore.Query{
		Namespace:  garden.Namespace,
		Collection: garden.CollectionNotes,
		Filters:    []docstore.Filter{{Field: "seed", Op: docstore.OpEq, Value: seedID}},
		Sort:       &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
		Limit:      1,
	})
	if err != nil {
		return time.Time{}, err
	}
	if len(readNotes.Documents) == 0 {
		return time.Time{}, nil
	}
	return readNotes.Documents[0].CreatedAt, nil
}

func (d *Daemon) handleSeedShow(conn net.Conn, msg *protocol.SeedShowMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "show", err)
		return
	}
	seed, doc, err := d.readSeed(msg.SeedID)
	if err != nil {
		d.sendGardenError(conn, "show", err)
		return
	}
	notes, total, err := d.readNotes(seed.ID, garden.ShowNotes)
	if err != nil {
		d.logf("garden: reading the log of %s: %v", seed.ID, err)
	}
	read, err := d.readGarden()
	if err != nil {
		d.logf("garden: reading the garden around %s: %v", seed.ID, err)
	}
	wire := seedToProtocol(seed, doc, read.ready[seed.ID])
	d.decorateSeedContinuation(&wire, seed)
	if progress, ok := read.progress(seed.ID); ok {
		wire.PlotProgress = progress
	}
	sessionID := strings.TrimSpace(protocol.Deref(msg.SourceSessionID))
	watching := d.seedWatching(sessionID, seed.ID)
	artifacts, err := d.seedArtifacts(seed.ID)
	if err != nil {
		d.sendGardenError(conn, "show", fmt.Errorf("read seed artifacts: %w", err))
		return
	}
	d.consumeSeedBell(sessionID, seed.ID)
	d.sendGardenResponse(conn, protocol.Response{
		Ok: true,
		SeedShowResult: &protocol.SeedShowResult{
			Seed:       wire,
			Watching:   watching,
			Notes:      notes,
			NotesTotal: total,
			Relations:  gardenRelations(read, seed.ID),
			Handoff:    d.gardenHandoff(seed.ID),
			Artifacts:  artifacts,
			References: d.seedArtifactReferences(seed.ID),
		},
	})
}

func (d *Daemon) handleSeedEdit(conn net.Conn, msg *protocol.SeedEditMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "edit", err)
		return
	}
	if err := garden.ValidateBody(msg.Body); err != nil {
		d.sendGardenError(conn, "edit", err)
		return
	}
	seed, doc, err := d.applySeedBodyEdit(msg.SeedID, msg.Body)
	if err != nil {
		d.sendGardenError(conn, "edit", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{
		Ok:             true,
		SeedEditResult: &protocol.SeedEditResult{Seed: seedToProtocol(seed, doc, d.gardenReady()[seed.ID])},
	})
}

func normalizeSeedResumeIdentity(sessionID, cwd, agent string) (string, string, string, error) {
	sessionID, cwd, agent = strings.TrimSpace(sessionID), strings.TrimSpace(cwd), strings.TrimSpace(agent)
	present := 0
	for _, value := range []string{sessionID, cwd, agent} {
		if value != "" {
			present++
		}
	}
	if present != 0 && present != 3 {
		return "", "", "", fmt.Errorf("resume identity needs --resume-session-id, --cwd, and --agent together")
	}
	return sessionID, cwd, agent, nil
}

func (d *Daemon) handleSeedSetResume(conn net.Conn, msg *protocol.SeedSetResumeMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "set-resume", err)
		return
	}
	clear := protocol.Deref(msg.Clear)
	resumeID, cwd, agent, err := normalizeSeedResumeIdentity(
		protocol.Deref(msg.ResumeSessionID), protocol.Deref(msg.ResumeCwd), protocol.Deref(msg.ResumeAgent),
	)
	if err != nil {
		d.sendGardenError(conn, "set-resume", err)
		return
	}
	if clear {
		if resumeID != "" || cwd != "" || agent != "" {
			d.sendGardenError(conn, "set-resume", fmt.Errorf("--clear cannot be combined with resume identity fields"))
			return
		}
	} else if resumeID == "" {
		d.sendGardenError(conn, "set-resume", fmt.Errorf("nothing to set — pass --resume-session-id, --cwd, and --agent together, or --clear"))
		return
	}
	seed, doc, err := d.applySeedResumeIdentity(msg.SeedID, resumeID, cwd, agent)
	if err != nil {
		d.sendGardenError(conn, "set-resume", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{
		Ok: true, SeedSetResumeResult: &protocol.SeedSetResumeResult{
			Seed: func() protocol.Seed {
				wire := seedToProtocol(seed, doc, d.gardenReady()[seed.ID])
				d.decorateSeedContinuation(&wire, seed)
				return wire
			}(),
		},
	})
}

func (d *Daemon) applySeedResumeIdentity(id, resumeID, cwd, agent string) (garden.Seed, docstore.Document, error) {
	schema, err := d.seedsCollection()
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(id)
		if err != nil {
			return garden.Seed{}, docstore.Document{}, err
		}
		seed.ResumeSessionID, seed.ResumeCwd, seed.ResumeAgent = resumeID, cwd, agent
		written, err := d.writeSeed(*schema, seed, doc.Rev, FactGardenResumeIdentityChanged)
		if err == nil {
			return seed, written, nil
		}
		if !docstore.IsConflict(err) {
			return garden.Seed{}, docstore.Document{}, err
		}
	}
	return garden.Seed{}, docstore.Document{}, fmt.Errorf(
		"%s was rewritten under all %d attempts to set its resume identity; read it again with `attn seed show %s` and retry",
		id, attempts, id)
}

func (d *Daemon) applySeedBodyEdit(id, body string) (garden.Seed, docstore.Document, error) {
	schema, err := d.seedsCollection()
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(id)
		if err != nil {
			return garden.Seed{}, docstore.Document{}, err
		}
		seed.Body = body
		written, err := d.writeSeed(*schema, seed, doc.Rev, FactGardenBodyEdited)
		if err == nil {
			return seed, written, nil
		}
		if !docstore.IsConflict(err) {
			return garden.Seed{}, docstore.Document{}, err
		}
	}
	return garden.Seed{}, docstore.Document{}, fmt.Errorf(
		"%s was rewritten under all %d attempts to edit it; read it again with `attn seed show %s` and retry",
		id, attempts, id)
}

func (d *Daemon) handleSeedDocumentGet(client *wsClient, msg *protocol.SeedDocumentGetMessage) {
	result := protocol.SeedDocumentGetResultMessage{
		Event:     protocol.EventSeedDocumentGetResult,
		RequestID: msg.RequestID,
	}
	fail := func(err error) {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
	}
	if err := d.requireHome(garden.Surface); err != nil {
		fail(err)
		return
	}
	seed, doc, err := d.readSeed(msg.SeedID)
	if err != nil {
		fail(err)
		return
	}
	read, err := d.readGarden()
	if err != nil {
		fail(err)
		return
	}
	children := make([]garden.Seed, 0)
	for _, candidate := range read.seeds {
		for _, edge := range candidate.Edges {
			if edge.Kind == garden.EdgePartOf && edge.To == seed.ID {
				children = append(children, candidate)
				break
			}
		}
	}
	notes, notesTotal, err := d.readNotes(seed.ID, docstore.MaxLimit)
	if err != nil {
		fail(err)
		return
	}
	wireSeed := seedToProtocol(seed, doc, read.ready[seed.ID])
	d.decorateSeedContinuation(&wireSeed, seed)
	if progress, ok := read.progress(seed.ID); ok {
		wireSeed.PlotProgress = progress
	}
	artifacts, err := d.seedArtifacts(seed.ID)
	if err != nil {
		fail(fmt.Errorf("read seed artifacts: %w", err))
		return
	}
	result.Document = &protocol.SeedDocument{
		Seed:        wireSeed,
		TenderHolds: seed.Tender().Holds(d.sessionExists),
		Children:    read.wire(children),
		Notes:       notes,
		NotesTotal:  notesTotal,
		Artifacts:   artifacts,
		References:  d.seedArtifactReferences(seed.ID),
	}
	result.Success = true
	d.sendToClient(client, result)
}

func gardenRelations(read gardenRead, id string) []protocol.SeedRelation {
	index := make(map[string]garden.Seed, len(read.seeds))
	for _, seed := range read.seeds {
		index[seed.ID] = seed
	}
	relations := garden.Relations(read.seeds, id)
	out := make([]protocol.SeedRelation, 0, len(relations))
	for _, relation := range relations {
		other := index[relation.Seed]
		out = append(out, protocol.SeedRelation{
			Label:  relation.Label,
			SeedID: relation.Seed,
			Title:  other.Title,
			Status: other.Status,
		})
	}
	return out
}

func (d *Daemon) handleSeedLink(conn net.Conn, msg *protocol.SeedLinkMessage) {
	verb := "link"
	if protocol.Deref(msg.Unlink) {
		verb = "unlink"
	}
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, verb, err)
		return
	}
	kind, err := garden.ParseEdgeKind(msg.Kind)
	if err != nil {
		d.sendGardenError(conn, verb, err)
		return
	}
	from := strings.TrimSpace(msg.SeedID)
	to := strings.TrimSpace(msg.ToSeedID)
	for _, id := range []string{from, to} {
		if err := garden.ValidateID(id); err != nil {
			d.sendGardenError(conn, verb, err)
			return
		}
	}
	schema, err := d.seedsCollection()
	if err != nil {
		d.sendGardenError(conn, verb, err)
		return
	}

	const attempts = 3
	for range attempts {
		read, err := d.readGarden()
		if err != nil {
			d.sendGardenError(conn, verb, err)
			return
		}
		var next garden.Seed
		changed := true
		if verb == "unlink" {
			next, err = garden.Unlink(read.seeds, from, kind, to)
		} else {
			next, changed, err = garden.Link(read.seeds, from, kind, to)
		}
		if err != nil {
			d.sendGardenError(conn, verb, err)
			return
		}
		if !changed {
			d.sendGardenResponse(conn, protocol.Response{
				Ok: true,
				SeedLinkResult: &protocol.SeedLinkResult{
					Seed: seedToProtocol(next, read.docs[next.ID], read.ready[next.ID]), Changed: false,
				},
			})
			return
		}
		fact := FactGardenLinked
		if verb == "unlink" {
			fact = FactGardenUnlinked
		}
		doc, err := d.writeSeed(*schema, next, read.docs[next.ID].Rev, fact)
		if err != nil {
			if docstore.IsConflict(err) {
				continue
			}
			d.sendGardenError(conn, verb, err)
			return
		}
		d.sendGardenResponse(conn, protocol.Response{
			Ok: true,
			SeedLinkResult: &protocol.SeedLinkResult{
				Seed: seedToProtocol(next, doc, d.gardenReady()[next.ID]), Changed: true,
			},
		})
		return
	}
	d.sendGardenError(conn, verb, fmt.Errorf(
		"%s was rewritten under all %d attempts to %s it; read it again with `attn seed show %s` and decide from what it says now",
		from, attempts, verb, from))
}

func (d *Daemon) handleSeedReady(conn net.Conn, msg *protocol.SeedReadyMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "ready", err)
		return
	}
	crown := strings.TrimSpace(protocol.Deref(msg.Plot))
	if crown != "" {
		if err := garden.ValidateID(crown); err != nil {
			d.sendGardenError(conn, "ready", err)
			return
		}
		if _, _, err := d.readSeed(crown); err != nil {
			d.sendGardenError(conn, "ready", err)
			return
		}
	}
	var result *protocol.SeedReadyResult
	var err error
	if crown == "" && !protocol.Deref(msg.All) {
		result, err = d.gardenPrime(strings.TrimSpace(protocol.Deref(msg.SourceSessionID)))
	} else {
		result, err = d.gardenReadyResult(crown)
	}
	if err != nil {
		d.sendGardenError(conn, "ready", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedReadyResult: result})
}

func (d *Daemon) gardenReadyResult(crown string) (*protocol.SeedReadyResult, error) {
	read, err := d.readGarden()
	if err != nil {
		return nil, err
	}
	ready := make([]garden.Seed, 0, len(read.ready))
	for _, seed := range read.seeds {
		if read.ready[seed.ID] {
			ready = append(ready, seed)
		}
	}

	result := &protocol.SeedReadyResult{Scope: "garden"}
	if crown != "" {
		inPlot := map[string]bool{}
		for _, seed := range garden.InPlot(read.seeds, crown) {
			inPlot[seed.ID] = true
		}
		scoped := make([]garden.Seed, 0, len(ready))
		for _, seed := range ready {
			if inPlot[seed.ID] {
				scoped = append(scoped, seed)
			}
		}
		ready, result.Scope, result.ScopeID = scoped, "plot", crown
		if crownSeed, crownDoc, err := d.readSeed(crown); err == nil {
			wire := seedToProtocol(crownSeed, crownDoc, read.ready[crown])
			if progress, ok := read.progress(crown); ok {
				wire.PlotProgress = progress
			}
			result.Crown = &wire
		}
	} else {
		selected := make(map[string]bool, len(ready))
		for _, seed := range ready {
			selected[seed.ID] = true
		}
		plots := garden.PlotHeaders(read.seeds, selected)
		slices.Reverse(plots)
		for _, plot := range plots {
			wire := seedToProtocol(plot, read.docs[plot.ID], read.ready[plot.ID])
			if progress, ok := read.progress(plot.ID); ok {
				wire.PlotProgress = progress
			}
			result.Plots = append(result.Plots, wire)
		}
	}
	slices.Reverse(ready)
	result.Seeds = read.wire(ready)
	if result.Scope == "plot" {
		for _, seed := range ready {
			if handoff := d.gardenHandoff(seed.ID); handoff != nil {
				result.Handoffs = append(result.Handoffs, *handoff)
			}
		}
	}
	return result, nil
}

func (d *Daemon) dispatchesCollection() (*docstore.CollectionSchema, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	return d.collectionFor(garden.Namespace, garden.CollectionDispatches)
}

func (d *Daemon) recordGardenDispatch(sessionID, crown, dispatcherSession, cwd, agent string, fromChief bool) error {
	sessionID = strings.TrimSpace(sessionID)
	observed := observedGardenExecution(&protocol.Session{
		ID: sessionID, Directory: cwd, Agent: protocol.SessionAgent(agent),
	}, "", d.gardenTime())
	if session := d.gardenSession(sessionID); session != nil {
		resumeID := ""
		if d.store.Get(sessionID) != nil {
			resumeID = d.store.GetResumeSessionID(sessionID)
		}
		observed = observedGardenExecution(session, resumeID, d.gardenTime())
	}
	_, err := d.updateGardenDispatch(sessionID, func(current garden.Dispatch) (garden.Dispatch, bool, error) {
		next := mergeGardenExecution(current, observed)
		if wanted := strings.TrimSpace(crown); wanted != "" {
			if successor := strings.TrimSpace(current.SupersededBy); successor != "" {
				return garden.Dispatch{}, false, fmt.Errorf(
					"session %s was already superseded by %s while binding it to %s", sessionID, successor, wanted)
			}
			next.Crown = wanted
		}
		if dispatcher := strings.TrimSpace(dispatcherSession); dispatcher != "" {
			next.DispatcherSession = dispatcher
		}
		next.FromChief = fromChief
		return next, true, nil
	})
	return err
}

func (d *Daemon) rememberDispatchResume(sessionID, resumeSessionID string) error {
	sessionID, resumeSessionID = strings.TrimSpace(sessionID), strings.TrimSpace(resumeSessionID)
	if sessionID == "" || resumeSessionID == "" {
		return nil
	}
	_, err := d.updateGardenDispatch(sessionID, func(current garden.Dispatch) (garden.Dispatch, bool, error) {
		if current.Resume == resumeSessionID {
			return current, false, nil
		}
		current.Resume = resumeSessionID
		return current, true, nil
	})
	if err != nil {
		d.logf("garden: recording the resume id for session %s: %v", sessionID, err)
	}
	return err
}

func (d *Daemon) gardenDispatchResume(sessionID string) string {
	dispatch, ok := d.gardenDispatch(sessionID)
	if !ok {
		return ""
	}
	return strings.TrimSpace(dispatch.Resume)
}

func (d *Daemon) validateDispatchCrown(crown, sourceSessionID string) error {
	if crown == "" {
		return nil
	}
	if err := d.requireHome(garden.Surface); err != nil {
		return fmt.Errorf("dispatch at %s: %w", crown, err)
	}
	seed, _, err := d.readSeed(crown)
	if err != nil {
		return err
	}
	if garden.Closed(seed.Status) {
		return fmt.Errorf(
			"%s is %s, so there is no open work to dispatch at; reopen it first with `attn seed replant %s`",
			crown, seed.Status, crown)
	}
	held := seed.Tender()
	if held.Holds(d.sessionExists) && !held.Is(garden.Tender{Session: strings.TrimSpace(sourceSessionID)}) {
		return fmt.Errorf(
			"%s is being tended by %s, and a seed has one tender at a time; dispatching here would hand it to a new agent.\n"+
				"Wait for %s to harvest or park it, plant the work as its own seed, or say what you need on the log: attn seed note %s -m \"…\"",
			crown, held.DisplayName(), held.DisplayName(), crown)
	}
	return nil
}

func (d *Daemon) gardenDispatch(sessionID string) (garden.Dispatch, bool) {
	if sessionID == "" || d.store == nil {
		return garden.Dispatch{}, false
	}
	schema, err := d.dispatchesCollection()
	if err != nil {
		return garden.Dispatch{}, false
	}
	doc, found, err := d.store.GetDocument(*schema, sessionID)
	if err != nil || !found {
		return garden.Dispatch{}, false
	}
	dispatch, err := garden.DecodeDispatch(doc.Body)
	if err != nil {
		d.logf("garden: dispatch record for %s has an unreadable body: %v", sessionID, err)
		return garden.Dispatch{}, false
	}
	return dispatch, true
}

func (d *Daemon) gardenDispatchCrown(sessionID string) (string, bool) {
	dispatch, ok := d.gardenDispatch(sessionID)
	if !ok {
		return "", false
	}
	crown := activeDispatchCrown(dispatch)
	return crown, crown != ""
}

func (d *Daemon) gardenDispatchSeedsBySession() map[string]string {
	if d.store == nil {
		return nil
	}
	d.dispatchSeedsMu.Lock()
	defer d.dispatchSeedsMu.Unlock()
	if !d.dispatchSeedsLoaded {
		read, _, err := d.runDocQuery(docstore.Query{
			Namespace:  garden.Namespace,
			Collection: garden.CollectionDispatches,
		})
		if err != nil {
			if !docstore.IsUndeclaredCollection(err) {
				d.logf("garden: reading dispatch records for broadcast: %v", err)
				return nil
			}
			d.dispatchSeeds, d.dispatchFromChief, d.dispatchProjectionRevs, d.dispatchSeedsLoaded = nil, nil, nil, true
			return nil
		}
		loaded := make(map[string]string, len(read.Documents))
		fromChief := map[string]bool{}
		revisions := make(map[string]int64, len(read.Documents))
		for _, doc := range read.Documents {
			revisions[doc.ID] = doc.Rev
			dispatch, err := garden.DecodeDispatch(doc.Body)
			if err != nil || activeDispatchCrown(dispatch) == "" {
				continue
			}
			loaded[doc.ID] = activeDispatchCrown(dispatch)
			if dispatch.FromChief {
				fromChief[doc.ID] = true
			}
		}
		d.dispatchSeeds, d.dispatchFromChief, d.dispatchProjectionRevs = loaded, fromChief, revisions
		d.dispatchSeedsLoaded = true
	}
	return d.dispatchSeeds
}

func (d *Daemon) rememberDispatchProjection(sessionID string, dispatch garden.Dispatch, rev int64) {
	d.dispatchSeedsMu.Lock()
	defer d.dispatchSeedsMu.Unlock()
	if !d.dispatchSeedsLoaded {
		return
	}
	if current, ok := d.dispatchProjectionRevs[sessionID]; ok && rev < current {
		return
	}
	nextSeeds := make(map[string]string, len(d.dispatchSeeds)+1)
	for id, seed := range d.dispatchSeeds {
		nextSeeds[id] = seed
	}
	if crown := activeDispatchCrown(dispatch); crown != "" {
		nextSeeds[sessionID] = crown
	} else {
		delete(nextSeeds, sessionID)
	}
	nextChief := make(map[string]bool, len(d.dispatchFromChief)+1)
	for id := range d.dispatchFromChief {
		nextChief[id] = true
	}
	if dispatch.FromChief {
		nextChief[sessionID] = true
	} else {
		delete(nextChief, sessionID)
	}
	if d.dispatchProjectionRevs == nil {
		d.dispatchProjectionRevs = map[string]int64{}
	}
	d.dispatchSeeds = nextSeeds
	d.dispatchFromChief = nextChief
	d.dispatchProjectionRevs[sessionID] = rev
}

func (d *Daemon) gardenDispatchesFromChief() map[string]bool {
	d.gardenDispatchSeedsBySession()
	d.dispatchSeedsMu.Lock()
	defer d.dispatchSeedsMu.Unlock()
	return d.dispatchFromChief
}

func (d *Daemon) decorateSessionSeed(session *protocol.Session, seedBySession map[string]string) {
	if session == nil {
		return
	}
	if seed := seedBySession[session.ID]; seed != "" {
		session.SeedID = protocol.Ptr(seed)
		return
	}
	session.SeedID = nil
}

func (d *Daemon) gardenPrime(sessionID string) (*protocol.SeedReadyResult, error) {
	if err := d.requireHome(garden.Surface); err != nil {
		return nil, err
	}
	crown := ""
	if at, ok := d.gardenDispatchCrown(strings.TrimSpace(sessionID)); ok {
		if _, _, err := d.readSeed(at); err == nil {
			crown = at
		}
	}
	return d.gardenReadyResult(crown)
}

var gardenFacts = map[garden.Verb]string{
	garden.VerbTend:    FactGardenTended,
	garden.VerbPark:    FactGardenParked,
	garden.VerbHarvest: FactGardenHarvested,
	garden.VerbWither:  FactGardenWithered,
	garden.VerbReplant: FactGardenReplanted,
}

func (d *Daemon) handleSeedTransition(conn net.Conn, msg *protocol.SeedTransitionMessage) {
	verb, err := garden.ParseVerb(msg.Verb)
	if err != nil {
		d.sendGardenError(conn, strings.TrimSpace(msg.Verb), err)
		return
	}
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, string(verb), err)
		return
	}
	sessionID := strings.TrimSpace(protocol.Deref(msg.SourceSessionID))
	memberName := strings.TrimSpace(protocol.Deref(msg.Member))
	actorSession := sessionID
	if memberName != "" {
		actorSession = ""
	}
	actor := garden.Tender{
		Session: actorSession,
		Member:  d.resolveTenderMember(memberName, sessionID),
	}
	ask := garden.Ask{
		Actor:  actor,
		Reason: protocol.Deref(msg.Reason),
		Force:  protocol.Deref(msg.Force),
	}
	if harvestWhenRequested(msg) {
		seed, doc, err := d.applyHarvestWhenRequest(msg, verb, ask, sessionID)
		if err != nil {
			d.sendGardenError(conn, string(verb), err)
			return
		}
		d.sendGardenResponse(conn, protocol.Response{
			Ok:                   true,
			SeedTransitionResult: &protocol.SeedTransitionResult{Seed: d.seedTransitionWire(seed, doc)},
		})
		return
	}
	seed, doc, notes, err := d.applySeedTransitionDetailed(
		msg.SeedID, verb, ask, protocol.Deref(msg.Comment))
	if err != nil {
		d.sendGardenError(conn, string(verb), err)
		return
	}
	for _, note := range notes.all() {
		d.mirrorSeedNoteOntoTicket(sessionID, seed.ID, note.Body)
	}
	result := &protocol.SeedTransitionResult{Seed: d.seedTransitionWire(seed, doc)}
	if verb == garden.VerbTend {
		result.Handoff = d.gardenHandoff(seed.ID)
	}
	// Mirrored before the response: a caller that harvests and then reads the board
	// must not see the ticket mid-flight.
	d.mirrorSeedMoveOntoTicket(sessionID, seed.ID, verb, protocol.Deref(msg.Reason))
	d.ringSeedActivity(seed.ID, gardenRingEvents[verb], sessionID)
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedTransitionResult: result})
}

// The seed a move leaves behind, plus the two fields that only a read of the
// whole garden can fill in.
func (d *Daemon) seedTransitionWire(seed garden.Seed, doc docstore.Document) protocol.Seed {
	wire := seedToProtocol(seed, doc, false)
	if read, err := d.readGarden(); err == nil {
		wire.Ready = read.ready[seed.ID]
		if progress, ok := read.progress(seed.ID); ok {
			wire.PlotProgress = progress
		}
	}
	return wire
}

func (d *Daemon) applySeedTransition(id string, verb garden.Verb, ask garden.Ask) (garden.Seed, docstore.Document, error) {
	seed, doc, _, err := d.applySeedTransitionDetailedAs(id, verb, ask, "", d.sessionExists)
	return seed, doc, err
}

type seedTransitionNotes struct {
	Audit   *protocol.SeedNote
	Comment *protocol.SeedNote
}

func (n seedTransitionNotes) all() []*protocol.SeedNote {
	var notes []*protocol.SeedNote
	if n.Audit != nil {
		notes = append(notes, n.Audit)
	}
	if n.Comment != nil {
		notes = append(notes, n.Comment)
	}
	return notes
}

func (d *Daemon) applySeedTransitionDetailed(
	id string, verb garden.Verb, ask garden.Ask, comment string,
) (garden.Seed, docstore.Document, seedTransitionNotes, error) {
	return d.applySeedTransitionDetailedAtRevision(id, verb, ask, comment, 0)
}

func (d *Daemon) applySeedTransitionDetailedAtRevision(
	id string, verb garden.Verb, ask garden.Ask, comment string, expectedRev int64,
) (garden.Seed, docstore.Document, seedTransitionNotes, error) {
	return d.applySeedTransitionDetailedAsAtRevision(id, verb, ask, comment, d.sessionExists, expectedRev)
}

func (d *Daemon) applySeedTransitionAs(
	id string, verb garden.Verb, ask garden.Ask, sessionLive func(string) bool,
) (garden.Seed, docstore.Document, error) {
	seed, doc, _, err := d.applySeedTransitionDetailedAsAtRevision(id, verb, ask, "", sessionLive, 0)
	return seed, doc, err
}

func (d *Daemon) applySeedTransitionDetailedAs(
	id string, verb garden.Verb, ask garden.Ask, comment string, sessionLive func(string) bool,
) (garden.Seed, docstore.Document, seedTransitionNotes, error) {
	return d.applySeedTransitionDetailedAsAtRevision(id, verb, ask, comment, sessionLive, 0)
}

func (d *Daemon) applySeedTransitionDetailedAsAtRevision(
	id string, verb garden.Verb, ask garden.Ask, comment string, sessionLive func(string) bool, expectedRev int64,
) (garden.Seed, docstore.Document, seedTransitionNotes, error) {
	comment = strings.TrimSpace(comment)
	if comment != "" && verb != garden.VerbPark {
		return garden.Seed{}, docstore.Document{}, seedTransitionNotes{}, fmt.Errorf(
			"a lifecycle comment belongs to Park; use `attn seed note %s -m \"…\"` for %s", id, verb)
	}
	if comment != "" {
		if err := garden.ValidateNote(comment); err != nil {
			return garden.Seed{}, docstore.Document{}, seedTransitionNotes{}, err
		}
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return garden.Seed{}, docstore.Document{}, seedTransitionNotes{}, err
	}
	fact, ok := gardenFacts[verb]
	if !ok {
		return garden.Seed{}, docstore.Document{}, seedTransitionNotes{}, fmt.Errorf("no bus fact is declared for %q", verb)
	}
	// A conflict means the seed moved between read and write; re-reading turns a
	// lost race into the honest answer. Tripwire: two agents contending is one retry.
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(id)
		if err != nil {
			return garden.Seed{}, docstore.Document{}, seedTransitionNotes{}, err
		}
		if expectedRev > 0 && doc.Rev != expectedRev {
			return garden.Seed{}, docstore.Document{}, seedTransitionNotes{}, fmt.Errorf(
				"%s changed since you reviewed it; refresh the garden", id)
		}
		var displaced *garden.Tender
		if held := seed.Tender(); ask.Force && held.Holds(sessionLive) && !held.Is(ask.Actor) {
			displaced = &held
		}
		next, err := garden.Transition(seed, verb, ask, sessionLive)
		if err != nil {
			return garden.Seed{}, docstore.Document{}, seedTransitionNotes{}, err
		}
		if next.Status != seed.Status {
			next.StateChangedAt = formatGardenTime(d.gardenTime())
		}
		if verb == garden.VerbTend && strings.TrimSpace(ask.Actor.Session) != "" {
			execution, executionErr := d.ensureGardenExecution(ask.Actor.Session)
			if executionErr != nil {
				return garden.Seed{}, docstore.Document{}, seedTransitionNotes{}, executionErr
			}
			next.LastExecutionID = execution.SessionID
		}
		var written docstore.Document
		notes := seedTransitionNotes{}
		if displaced == nil && comment == "" {
			written, err = d.writeSeed(*schema, next, doc.Rev, fact)
		} else {
			var entries []garden.Note
			auditIndex, commentIndex := -1, -1
			if displaced != nil {
				auditIndex = len(entries)
				entries = append(entries, garden.Note{
					Seed: next.ID, Kind: garden.NoteKindNote,
					Body:          forcedSeedMoveBody(next.ID, verb, ask.Actor, *displaced),
					AuthorSession: ask.Actor.Session, AuthorMember: ask.Actor.Member,
				})
			}
			if comment != "" {
				commentIndex = len(entries)
				entries = append(entries, garden.Note{
					Seed: next.ID, Kind: garden.NoteKindNote, Body: comment,
					AuthorSession: ask.Actor.Session, AuthorMember: ask.Actor.Member,
				})
			}
			var writtenNotes []protocol.SeedNote
			written, writtenNotes, err = d.writeSeedMoveWithNotes(*schema, next, doc.Rev, fact, entries)
			if err == nil {
				if auditIndex >= 0 {
					notes.Audit = &writtenNotes[auditIndex]
				}
				if commentIndex >= 0 {
					notes.Comment = &writtenNotes[commentIndex]
				}
			}
		}
		if err == nil {
			return next, written, notes, nil
		}
		if !docstore.IsConflict(err) {
			return garden.Seed{}, docstore.Document{}, seedTransitionNotes{}, err
		}
		if expectedRev > 0 {
			return garden.Seed{}, docstore.Document{}, seedTransitionNotes{}, fmt.Errorf(
				"%s changed while the reviewed action was being applied; refresh the garden", id)
		}
	}
	return garden.Seed{}, docstore.Document{}, seedTransitionNotes{}, fmt.Errorf(
		"%s was rewritten under all %d attempts to %s it; read it again with `attn seed show %s` and decide from what it says now",
		id, attempts, verb, id)
}

func forcedSeedMoveBody(seedID string, verb garden.Verb, actor, displaced garden.Tender) string {
	forcedBy := actor.DisplayName()
	if forcedBy == "" {
		forcedBy = "the attn app"
	}
	return fmt.Sprintf("%s forced `attn seed %s %s`; %s held the seed.",
		forcedBy, verb, seedID, displaced.DisplayName())
}

func (d *Daemon) writeSeedMoveWithNotes(
	seedSchema docstore.CollectionSchema,
	seed garden.Seed,
	expected int64,
	transitionFact string,
	notes []garden.Note,
) (docstore.Document, []protocol.SeedNote, error) {
	noteSchema, err := d.notesCollection()
	if err != nil {
		return docstore.Document{}, nil, err
	}
	seedBody, err := seed.Encode()
	if err != nil {
		return docstore.Document{}, nil, err
	}
	seedChanged := documentChangedFact(garden.Namespace, garden.CollectionSeeds, seed.ID, false)

	const mintAttempts = 3
	var lastErr error
	for range mintAttempts {
		commits := make([]store.DocumentCommit, 1, len(notes)+1)
		commits[0] = store.DocumentCommit{
			Write: store.DocumentWrite{
				Schema: seedSchema, ID: seed.ID, Body: seedBody, Expected: &expected,
			},
			Fact: seedChanged,
		}
		noteBodies := make([][]byte, len(notes))
		noteFacts := make([]store.BusEvent, len(notes))
		for i := range notes {
			notes[i].ID, err = d.mintNoteID()
			if err != nil {
				return docstore.Document{}, nil, err
			}
			noteBodies[i], err = notes[i].Encode()
			if err != nil {
				return docstore.Document{}, nil, err
			}
			noteExpected := docstore.ExpectAbsent
			noteFacts[i] = documentChangedFact(garden.Namespace, garden.CollectionNotes, notes[i].ID, false)
			commits = append(commits, store.DocumentCommit{
				Write: store.DocumentWrite{
					Schema: *noteSchema, ID: notes[i].ID, Body: noteBodies[i], Expected: &noteExpected,
				},
				Fact: noteFacts[i],
			})
		}
		now := d.gardenTime()
		written, err := d.store.CommitDocumentWrites(commits, now)
		if err != nil {
			var conflict *docstore.ConflictError
			if errors.As(err, &conflict) && conflict.Namespace == garden.Namespace &&
				conflict.Collection == garden.CollectionNotes {
				lastErr = err
				continue
			}
			return docstore.Document{}, nil, err
		}

		d.announceCommittedWrite(seedChanged, written[0].Seq)
		d.publishFact(transitionFact, seed.ID, nil)
		for i := range notes {
			d.announceCommittedWrite(noteFacts[i], written[i+1].Seq)
			d.publishFact(FactGardenNoted, seed.ID, nil)
		}

		seedDoc, found, readErr := d.store.GetDocument(seedSchema, seed.ID)
		if readErr != nil || !found {
			seedDoc = &docstore.Document{
				ID: seed.ID, Body: seedBody, Rev: written[0].Rev, CreatedAt: now, UpdatedAt: now,
			}
		}
		wireNotes := make([]protocol.SeedNote, len(notes))
		for i := range notes {
			noteDoc, found, readErr := d.store.GetDocument(*noteSchema, notes[i].ID)
			if readErr != nil || !found {
				noteDoc = &docstore.Document{
					ID: notes[i].ID, Body: noteBodies[i], Rev: written[i+1].Rev, CreatedAt: now, UpdatedAt: now,
				}
			}
			wireNotes[i] = noteToProtocol(notes[i], *noteDoc)
		}
		return *seedDoc, wireNotes, nil
	}
	return docstore.Document{}, nil, fmt.Errorf(
		"minted %d note ids and every one was taken, which a working random source does not do: %v", mintAttempts, lastErr)
}

func (d *Daemon) notesCollection() (*docstore.CollectionSchema, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	return d.collectionFor(garden.Namespace, garden.CollectionNotes)
}

func (d *Daemon) handleSeedNote(conn net.Conn, msg *protocol.SeedNoteMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "note", err)
		return
	}
	authorSession := strings.TrimSpace(protocol.Deref(msg.SourceSessionID))
	note, err := d.appendSeedNote(
		msg.SeedID,
		msg.Body,
		authorSession,
		protocol.Deref(msg.Member),
		protocol.Deref(msg.Kind),
		artifactFromProtocol(msg.Artifact),
	)
	if err != nil {
		d.sendGardenError(conn, "note", err)
		return
	}
	d.mirrorSeedNoteOntoTicket(authorSession, msg.SeedID, note.Body)
	if protocol.Deref(msg.Ring) {
		d.ringSeedActivity(msg.SeedID, "note", authorSession)
	}
	d.sendGardenResponse(conn, protocol.Response{
		Ok:             true,
		SeedNoteResult: &protocol.SeedNoteResult{Note: note},
	})
}

func resolveNoteArtifact(kind string, artifact *garden.ArtifactReference, body string) (*garden.ArtifactReference, string, error) {
	if !garden.CarriesArtifact(kind) {
		if artifact != nil {
			return nil, "", fmt.Errorf(
				"a %s note carries no artifact; `attn seed attach` and `attn seed detach` are what associate a document", kind)
		}
		return nil, body, nil
	}
	if artifact == nil {
		return nil, "", fmt.Errorf("a %s note needs the artifact it %ses; the kinds are %s",
			kind, kind, strings.Join(garden.ArtifactKinds, ", "))
	}
	validated, err := garden.ValidateArtifact(*artifact)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(body) == "" {
		body = garden.DefaultNoteBody(kind, validated)
	}
	return &validated, body, nil
}

func (d *Daemon) appendSeedNote(seedID, body, authorSession, member, kindName string, artifact *garden.ArtifactReference) (protocol.SeedNote, error) {
	kind, err := garden.ParseNoteKind(kindName)
	if err != nil {
		return protocol.SeedNote{}, err
	}
	artifact, body, err = resolveNoteArtifact(kind, artifact, body)
	if err != nil {
		return protocol.SeedNote{}, err
	}
	if err := garden.ValidateNote(body); err != nil {
		return protocol.SeedNote{}, err
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return protocol.SeedNote{}, err
	}
	schema, err := d.notesCollection()
	if err != nil {
		return protocol.SeedNote{}, err
	}
	authorSession = strings.TrimSpace(authorSession)
	note := garden.Note{
		Seed:          seed.ID,
		Kind:          kind,
		Body:          body,
		AuthorSession: authorSession,
		AuthorMember:  d.resolveTenderMember(member, authorSession),
		Artifact:      artifact,
	}
	written, doc, err := d.mintAndWriteNote(*schema, note)
	if err != nil {
		return protocol.SeedNote{}, err
	}
	return noteToProtocol(written, doc), nil
}

func (d *Daemon) mintAndWriteNote(schema docstore.CollectionSchema, note garden.Note) (garden.Note, docstore.Document, error) {
	const mintAttempts = 3
	var lastErr error
	for range mintAttempts {
		id, err := d.mintNoteID()
		if err != nil {
			return note, docstore.Document{}, err
		}
		note.ID = id
		body, err := note.Encode()
		if err != nil {
			return note, docstore.Document{}, err
		}
		expected := docstore.ExpectAbsent
		fact := documentChangedFact(garden.Namespace, garden.CollectionNotes, note.ID, false)
		written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
			Schema: schema, ID: note.ID, Body: body, Expected: &expected,
		}, fact, time.Now())
		if err != nil {
			if !docstore.IsConflict(err) {
				return note, docstore.Document{}, err
			}
			lastErr = err
			continue
		}
		d.announceCommittedWrite(fact, written.Seq)
		d.publishFact(FactGardenNoted, note.Seed, nil)

		doc, found, err := d.store.GetDocument(schema, note.ID)
		if err != nil || !found {
			return note, docstore.Document{ID: note.ID, Body: body, Rev: written.Rev}, nil
		}
		return note, *doc, nil
	}
	return note, docstore.Document{}, fmt.Errorf(
		"minted %d note ids and every one was taken, which a working random source does not do: %w", mintAttempts, lastErr)
}

func (d *Daemon) mintNoteID() (string, error) {
	if d.gardenMintNoteID != nil {
		return d.gardenMintNoteID()
	}
	return garden.NewNoteID()
}

func (d *Daemon) handleSeedNotes(conn net.Conn, msg *protocol.SeedNotesMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "notes", err)
		return
	}
	seed, _, err := d.readSeed(msg.SeedID)
	if err != nil {
		d.sendGardenError(conn, "notes", err)
		return
	}
	limit := gardenSnapshotLimit
	if msg.Limit != nil && *msg.Limit > 0 {
		limit = *msg.Limit
	}
	notes, total, err := d.readNotes(seed.ID, limit)
	if err != nil {
		d.sendGardenError(conn, "notes", err)
		return
	}
	d.consumeSeedBell(protocol.Deref(msg.SourceSessionID), seed.ID)
	d.sendGardenResponse(conn, protocol.Response{
		Ok:              true,
		SeedNotesResult: &protocol.SeedNotesResult{Notes: notes, Total: total},
	})
}

func (d *Daemon) readNotes(seedID string, limit int) ([]protocol.SeedNote, int, error) {
	if d.store == nil {
		return nil, 0, errors.New("no database")
	}
	filter := docstore.Filter{Field: "seed", Op: docstore.OpEq, Value: seedID}
	read, _, err := d.runDocQuery(docstore.Query{
		Namespace:  garden.Namespace,
		Collection: garden.CollectionNotes,
		Filters:    []docstore.Filter{filter},
		Sort:       &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
		Limit:      limit,
	})
	if err != nil {
		return nil, 0, err
	}
	notes := make([]protocol.SeedNote, 0, len(read.Documents))
	for _, doc := range read.Documents {
		note, err := garden.DecodeNote(doc.Body)
		if err != nil {
			d.logf("garden: note %s has an unreadable body: %v", doc.ID, err)
			continue
		}
		notes = append(notes, noteToProtocol(note, doc))
	}
	total := len(notes)
	counted, found, err := d.store.CountQuery(docstore.Query{
		Namespace: garden.Namespace, Collection: garden.CollectionNotes,
		Filters: []docstore.Filter{filter},
	})
	if err == nil && found {
		total = counted.Count
	}
	return notes, total, nil
}

func (d *Daemon) readNotesDomain(seedID string) ([]garden.Note, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	page := d.gardenNotePageSize
	if page <= 0 {
		page = docstore.MaxLimit
	}
	notes := []garden.Note{}
	after := ""
	for {
		read, _, err := d.runDocQuery(docstore.Query{
			Namespace:  garden.Namespace,
			Collection: garden.CollectionNotes,
			Filters:    []docstore.Filter{{Field: "seed", Op: docstore.OpEq, Value: seedID}},
			Sort:       &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
			Limit:      page,
			After:      after,
		})
		if err != nil {
			return nil, err
		}
		for _, doc := range read.Documents {
			note, err := garden.DecodeNote(doc.Body)
			if err != nil {
				d.logf("garden: note %s has an unreadable body: %v", doc.ID, err)
				continue
			}
			notes = append(notes, note)
		}
		if len(read.Documents) < page {
			return notes, nil
		}
		after = read.Documents[len(read.Documents)-1].ID
	}
}

func (d *Daemon) freshestHandoff(seedID string) (*protocol.SeedNote, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	read, _, err := d.runDocQuery(docstore.Query{
		Namespace:  garden.Namespace,
		Collection: garden.CollectionNotes,
		Filters: []docstore.Filter{
			{Field: "seed", Op: docstore.OpEq, Value: seedID},
			{Field: "kind", Op: docstore.OpEq, Value: garden.NoteKindHandoff},
		},
		Sort:  &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
		Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(read.Documents) == 0 {
		return nil, nil
	}
	doc := read.Documents[0]
	note, err := garden.DecodeNote(doc.Body)
	if err != nil {
		return nil, fmt.Errorf("note %s has an unreadable body: %w", doc.ID, err)
	}
	wire := noteToProtocol(note, doc)
	return &wire, nil
}

func (d *Daemon) gardenHandoff(seedID string) *protocol.SeedNote {
	handoff, err := d.freshestHandoff(seedID)
	if err != nil {
		d.logf("garden: reading the freshest handoff on %s: %v", seedID, err)
		return nil
	}
	return handoff
}

func noteToProtocol(note garden.Note, doc docstore.Document) protocol.SeedNote {
	wire := protocol.SeedNote{
		ID:            note.ID,
		SeedID:        note.Seed,
		Kind:          note.Kind,
		Body:          note.Body,
		AuthorSession: note.AuthorSession,
		AuthorMember:  note.AuthorMember,
		CreatedAt:     doc.CreatedAt.UTC().Format(time.RFC3339),
	}
	if note.Artifact != nil {
		wire.Artifact = artifactToProtocol(*note.Artifact)
	}
	return wire
}

func artifactToProtocol(a garden.ArtifactReference) *protocol.SeedArtifactReference {
	wire := &protocol.SeedArtifactReference{Kind: a.Kind}
	if a.NotebookDocumentID != "" {
		wire.NotebookDocumentID = protocol.Ptr(a.NotebookDocumentID)
	}
	if a.Repository != "" {
		wire.Repository = protocol.Ptr(a.Repository)
	}
	if a.Path != "" {
		wire.Path = protocol.Ptr(a.Path)
	}
	if a.URL != "" {
		wire.URL = protocol.Ptr(a.URL)
	}
	return wire
}

func artifactFromProtocol(wire *protocol.SeedArtifactReference) *garden.ArtifactReference {
	if wire == nil {
		return nil
	}
	return &garden.ArtifactReference{
		Kind:               wire.Kind,
		NotebookDocumentID: protocol.Deref(wire.NotebookDocumentID),
		Repository:         protocol.Deref(wire.Repository),
		Path:               protocol.Deref(wire.Path),
		URL:                protocol.Deref(wire.URL),
	}
}

func (d *Daemon) sendGardenResponse(conn net.Conn, resp protocol.Response) {
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		d.logf("garden: writing response: %v", err)
	}
}
