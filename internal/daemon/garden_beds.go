package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

type gardenBeds struct {
	beds []garden.Bed
	docs map[string]docstore.Document
	byID map[string]garden.Bed
}

func (b gardenBeds) sortByName() {
	slices.SortFunc(b.beds, func(x, y garden.Bed) int {
		if c := strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)); c != 0 {
			return c
		}
		return strings.Compare(x.ID, y.ID)
	})
}

func (b gardenBeds) inProfile(profileID string) ([]garden.Bed, map[string]docstore.Document, map[string]garden.Bed) {
	if profileID == "" {
		return b.beds, b.docs, b.byID
	}
	beds := make([]garden.Bed, 0, len(b.beds))
	docs := make(map[string]docstore.Document, len(b.beds))
	byID := make(map[string]garden.Bed, len(b.beds))
	for _, bed := range b.beds {
		if bed.ProfileID != profileID {
			continue
		}
		beds = append(beds, bed)
		docs[bed.ID] = b.docs[bed.ID]
		byID[bed.ID] = bed
	}
	return beds, docs, byID
}

func (d *Daemon) bedsCollection() (*docstore.CollectionSchema, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	return d.collectionFor(garden.Namespace, garden.CollectionBeds)
}

func (d *Daemon) rememberedGardenBeds() (gardenBeds, error) {
	schema, err := d.bedsCollection()
	if err != nil {
		if docstore.IsUndeclaredCollection(err) {
			return gardenBeds{docs: map[string]docstore.Document{}, byID: map[string]garden.Bed{}}, nil
		}
		return gardenBeds{}, err
	}
	return store.RememberDocuments(d.store, &d.gardenBeds, *schema, func() (gardenBeds, error) {
		return d.readGardenBeds(*schema)
	}, func(previous gardenBeds, written []string) (gardenBeds, error) {
		return d.refreshGardenBeds(*schema, previous, written)
	})
}

func (d *Daemon) readGardenBeds(schema docstore.CollectionSchema) (gardenBeds, error) {
	docs, err := d.store.ReadAllDocuments(schema)
	if err != nil {
		return gardenBeds{}, err
	}
	out := gardenBeds{
		beds: make([]garden.Bed, 0, len(docs)),
		docs: make(map[string]docstore.Document, len(docs)),
		byID: make(map[string]garden.Bed, len(docs)),
	}
	for _, doc := range docs {
		bed, err := garden.DecodeBed(doc.Body)
		if err != nil {
			d.logf("garden: bed %s has an unreadable body: %v", doc.ID, err)
			continue
		}
		out.beds = append(out.beds, bed)
		out.docs[bed.ID] = doc
		out.byID[bed.ID] = bed
	}
	out.sortByName()
	return out, nil
}

func (d *Daemon) refreshGardenBeds(schema docstore.CollectionSchema, previous gardenBeds, written []string) (gardenBeds, error) {
	out := gardenBeds{
		docs: maps.Clone(previous.docs),
		byID: maps.Clone(previous.byID),
	}
	if out.docs == nil {
		out.docs = map[string]docstore.Document{}
	}
	if out.byID == nil {
		out.byID = map[string]garden.Bed{}
	}
	changed := map[string]bool{}
	for _, id := range written {
		if changed[id] {
			continue
		}
		changed[id] = true
		delete(out.docs, id)
		delete(out.byID, id)
		doc, found, err := d.store.GetDocument(schema, id)
		if err != nil {
			return gardenBeds{}, err
		}
		if !found {
			continue
		}
		bed, err := garden.DecodeBed(doc.Body)
		if err != nil {
			d.logf("garden: bed %s has an unreadable body: %v", doc.ID, err)
			continue
		}
		out.docs[bed.ID] = *doc
		out.byID[bed.ID] = bed
	}
	out.beds = make([]garden.Bed, 0, len(out.byID))
	for _, bed := range out.byID {
		out.beds = append(out.beds, bed)
	}
	out.sortByName()
	return out, nil
}

func (d *Daemon) bedsInProfile(profileID string) ([]garden.Bed, map[string]docstore.Document, map[string]garden.Bed, error) {
	all, err := d.rememberedGardenBeds()
	if err != nil {
		return nil, nil, nil, err
	}
	beds, docs, byID := all.inProfile(profileID)
	return beds, docs, byID, nil
}

func (d *Daemon) bedToProtocol(bed garden.Bed, doc docstore.Document) protocol.GardenBed {
	out := protocol.GardenBed{
		ID:        bed.ID,
		ProfileID: bed.ProfileID,
		Name:      bed.Name,
		Slug:      bed.Slug,
		Repos:     d.bedRepoAddresses(bed.BedRepositories),
		CreatedAt: doc.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: doc.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if bed.Note != "" {
		out.Note = protocol.Ptr(bed.Note)
	}
	return out
}

func (d *Daemon) gardenBedsForBroadcast(profileID string) []protocol.GardenBed {
	if d.store == nil || profileID == "" || !d.on(setting.GardenV2Enabled) {
		return []protocol.GardenBed{}
	}
	beds, docs, _, err := d.bedsInProfile(profileID)
	if err != nil {
		d.logf("garden: reading beds for broadcast: %v", err)
		return []protocol.GardenBed{}
	}
	out := make([]protocol.GardenBed, 0, len(beds))
	for _, bed := range beds {
		out = append(out, d.bedToProtocol(bed, docs[bed.ID]))
	}
	return out
}

func isGardenBedsFact(subject string) bool {
	beds := docstore.Target(garden.Namespace, garden.CollectionBeds)
	return subject == beds || strings.HasPrefix(subject, beds+"/")
}

func (d *Daemon) projectGardenBeds() {
	if d.store == nil || d.wsHub == nil {
		return
	}
	d.projectSnapshot(snapshotGardenBeds, func() {
		byProfile := map[string][]protocol.GardenBed{}
		d.wsHub.ForEachClient(func(client *wsClient) {
			profileID := client.selectedProfile()
			if profileID == "" {
				return
			}
			beds, ok := byProfile[profileID]
			if !ok {
				beds = d.gardenBedsForBroadcast(profileID)
				byProfile[profileID] = beds
			}
			d.sendToClient(client, &protocol.GardenBedsUpdatedMessage{
				Event:     protocol.EventGardenBedsUpdated,
				ProfileID: profileID,
				Beds:      beds,
			})
		})
	})
}

func gardenV2OffRefusal() error {
	return &command.Refusal{Code: "setting_off", Message: setting.GardenV2Enabled.Refusal().Error()}
}

func (d *Daemon) bedRepoAddress(path garden.RepoPath) string {
	machine := path.Machine
	if d.machineFor(machine).IsLocal() {
		machine = "home"
	}
	return machine + ":" + path.Path
}

func (d *Daemon) bedRepoAddresses(repos garden.BedRepositories) []string {
	addresses := make([]string, 0, len(repos.Names)+len(repos.Paths))
	addresses = append(addresses, repos.Names...)
	for _, path := range repos.Paths {
		addresses = append(addresses, d.bedRepoAddress(path))
	}
	return addresses
}

func sameBedRepos(a, b garden.BedRepositories) bool {
	return slices.Equal(a.Names, b.Names) && slices.Equal(a.Paths, b.Paths)
}

func (d *Daemon) resolveBedRepos(ctx context.Context, caller command.Caller, raw []string, current []garden.RepoPath) (garden.BedRepositories, error) {
	previous := make(map[string]garden.RepoPath, len(current))
	for _, path := range current {
		previous[d.bedRepoAddress(path)] = path
	}
	names := make(map[string]bool, len(raw))
	paths := make(map[garden.RepoPath]bool, len(raw))
	var repos garden.BedRepositories
	for _, entry := range raw {
		repo, err := garden.ValidateBedRepo(entry)
		if err != nil {
			return garden.BedRepositories{}, err
		}
		if repo != "." && repo != ".." && repo != "~" && !strings.Contains(repo, "/") && !strings.HasSuffix(repo, ":~") {
			if key := strings.ToLower(repo); !names[key] {
				names[key] = true
				repos.Names = append(repos.Names, repo)
			}
			continue
		}
		path, kept := previous[repo]
		if !kept {
			path, err = d.resolveBedRepoPath(ctx, caller, repo)
			if err != nil {
				return garden.BedRepositories{}, err
			}
		}
		if !paths[path] {
			paths[path] = true
			repos.Paths = append(repos.Paths, path)
		}
	}
	if count := len(repos.Names) + len(repos.Paths); count > garden.MaxBedRepos {
		return garden.BedRepositories{}, protocol.Refuse(protocol.RefusalLimit, "a bed holds at most %d repos, asked for %d", garden.MaxBedRepos, count)
	}
	return repos, nil
}

func (d *Daemon) resolveBedRepoPath(ctx context.Context, caller command.Caller, repo string) (garden.RepoPath, error) {
	place := d.bedCallerPlace(ctx, caller)
	if host, path, qualified := strings.Cut(repo, ":"); qualified && !strings.Contains(host, "/") &&
		(filepath.IsAbs(path) || namesHome(path)) {
		if strings.EqualFold(host, "home") {
			place = d.here()
		} else {
			target, err := d.resolveOutpost(host)
			if err != nil {
				return garden.RepoPath{}, err
			}
			place = d.machineFor(target.ID)
		}
		repo = path
	}
	if repo == "~" {
		repo = "~/"
	}
	if !filepath.IsAbs(repo) && !namesHome(repo) {
		folder, _ := d.callerFolder(caller)
		if folder == "" {
			return garden.RepoPath{}, protocol.Refuse(protocol.RefusalInvalidInput, "repo %q is relative and this caller has no working directory; pass an absolute path", repo)
		}
		repo = filepath.Join(folder, repo)
	}
	switch {
	case place.IsRemote():
		inspected, err := d.inspectRemoteCheckout(ctx, place.Outpost(), repo)
		if err != nil {
			return garden.RepoPath{}, err
		}
		repo = inspected.Cwd
	case place.IsLocal():
		repo = place.normalize(repo).path
	default:
		return garden.RepoPath{}, protocol.Refuse(protocol.RefusalInvalidInput, "choose a machine for repository path %q", repo)
	}
	return garden.RepoPath{Path: repo, Machine: place.ID()}, nil
}

func (d *Daemon) bedCallerPlace(ctx context.Context, caller command.Caller) machine {
	if caller.Directory == "" && caller.Session != "" {
		return d.sessionPlace(caller.Session)
	}
	return d.originPlace(ctx)
}

func (d *Daemon) resolveBedInProfile(profileID, ref string) (garden.Bed, error) {
	beds, _, _, err := d.bedsInProfile(profileID)
	if err != nil {
		return garden.Bed{}, err
	}
	if bed, ok := garden.FindBed(beds, ref); ok {
		return bed, nil
	}
	if len(beds) == 0 {
		return garden.Bed{}, protocol.Refuse(protocol.RefusalNotFound,
			"no bed %q exists in this profile, and this profile has no beds yet; create one with {attn seed bed add \"<name>\"}", strings.TrimSpace(ref))
	}
	names := make([]string, len(beds))
	for i, bed := range beds {
		names[i] = bed.Name
	}
	return garden.Bed{}, protocol.Refuse(protocol.RefusalNotFound,
		"no bed %q exists in this profile; existing beds are %s ({attn seed bed ls})", strings.TrimSpace(ref), strings.Join(names, ", "))
}

func (d *Daemon) matchBedForRepo(beds []garden.Bed, repo normalizedPath) (garden.Bed, bool) {
	return garden.MatchBedForRepo(beds, repo.path, func(binding garden.RepoPath) bool {
		owner := d.machineFor(binding.Machine)
		if !repo.on.SameAs(owner) {
			return false
		}
		bound := normalizedPath{on: owner, path: binding.Path}
		if bound.path == "/" {
			return repo.Equal(bound)
		}
		return repo.AtOrBelow(bound)
	})
}

func (d *Daemon) callerRepoForBedRouting(ctx context.Context, sessionID protocol.SessionID, callerDir string) normalizedPath {
	if callerDir = strings.TrimSpace(callerDir); callerDir != "" {
		return d.bedRepoAt(ctx, d.originPlace(ctx), callerDir)
	}
	sessionID = protocol.TrimID(sessionID)
	session := d.store.Get(sessionID)
	if session == nil {
		return normalizedPath{}
	}
	place := d.sessionPlace(sessionID)
	if repo := cmp.Or(protocol.Deref(session.MainRepo), protocol.Deref(session.Repository)); repo != "" {
		return place.normalize(repo)
	}
	if repo := d.bedRepoAt(ctx, place, session.Directory); !repo.Empty() {
		return repo
	}
	return place.normalize(session.Directory)
}

func (d *Daemon) bedRepoAt(ctx context.Context, place machine, dir string) normalizedPath {
	if dir == "" {
		return normalizedPath{}
	}
	var inspected protocol.CheckoutInspection
	var err error
	switch {
	case place.IsRemote():
		inspected, err = d.inspectRemoteCheckout(ctx, place.Outpost(), dir)
	case place.IsLocal():
		inspected, err = d.inspectCheckout(ctx, protocol.CheckoutInspectMessage{Cwd: dir})
	default:
		return place.normalize(dir)
	}
	if err != nil {
		return normalizedPath{}
	}
	return normalizedPath{on: place, path: cmp.Or(inspected.MainRepo, inspected.RepoRoot, inspected.Cwd)}
}

func (d *Daemon) validateBedUniqueness(existing []garden.Bed, selfID, name, slug string, repos garden.BedRepositories) error {
	for _, other := range existing {
		if other.ID == selfID {
			continue
		}
		if strings.EqualFold(other.Name, name) || strings.EqualFold(other.Slug, slug) {
			return protocol.Refuse(protocol.RefusalConflict, "a bed named %q (%s) already exists in this profile", other.Name, other.ID)
		}
	}
	for _, other := range existing {
		if other.ID == selfID {
			continue
		}
		for _, name := range repos.Names {
			if slices.ContainsFunc(other.Names, func(value string) bool { return strings.EqualFold(name, value) }) {
				return protocol.Refuse(protocol.RefusalConflict, "repo %q is already assigned to bed %q (%s); remove it there first", name, other.Name, other.ID)
			}
		}
		for _, path := range repos.Paths {
			if slices.Contains(other.Paths, path) {
				return protocol.Refuse(protocol.RefusalConflict, "repo %q is already assigned to bed %q (%s); remove it there first", d.bedRepoAddress(path), other.Name, other.ID)
			}
		}
	}
	return nil
}

func (d *Daemon) seedBedList(profileID string) (*protocol.SeedBedListResult, error) {
	if !d.on(setting.GardenV2Enabled) {
		return nil, gardenV2OffRefusal()
	}
	beds, docs, _, err := d.bedsInProfile(profileID)
	if err != nil {
		return nil, err
	}
	allSeeds, err := d.rememberedGardenSeeds()
	if err != nil {
		return nil, err
	}
	scoped := allSeeds.inProfile(profileID)
	openCounts := map[string]int{}
	for _, seed := range scoped.seeds {
		if garden.Closed(seed.Status) {
			continue
		}
		if effID := garden.EffectiveBedID(seed, scoped.plots); effID != "" {
			openCounts[effID]++
		}
	}
	out := make([]protocol.SeedBedSummary, 0, len(beds))
	for _, bed := range beds {
		wire := d.bedToProtocol(bed, docs[bed.ID])
		out = append(out, protocol.SeedBedSummary{
			ID:        wire.ID,
			ProfileID: wire.ProfileID,
			Name:      wire.Name,
			Slug:      wire.Slug,
			Note:      wire.Note,
			Repos:     wire.Repos,
			CreatedAt: wire.CreatedAt,
			UpdatedAt: wire.UpdatedAt,
			OpenCount: openCounts[bed.ID],
		})
	}
	return &protocol.SeedBedListResult{Beds: out}, nil
}

func (d *Daemon) seedBedAdd(ctx context.Context, caller command.Caller, profileID, rawName, rawNote string, rawRepos []string) (*protocol.SeedBedResult, error) {
	if !d.on(setting.GardenV2Enabled) {
		return nil, gardenV2OffRefusal()
	}
	name, err := garden.ValidateBedName(rawName)
	if err != nil {
		return nil, err
	}
	note, err := garden.ValidateBedNote(rawNote)
	if err != nil {
		return nil, err
	}
	repos, err := d.resolveBedRepos(ctx, caller, rawRepos, nil)
	if err != nil {
		return nil, err
	}
	slug := garden.BedSlug(name)

	d.gardenBedsMu.Lock()
	defer d.gardenBedsMu.Unlock()

	existing, _, _, err := d.bedsInProfile(profileID)
	if err != nil {
		return nil, err
	}
	if err := d.validateBedUniqueness(existing, "", name, slug, repos); err != nil {
		return nil, err
	}
	schema, err := d.bedsCollection()
	if err != nil {
		return nil, err
	}
	const mintAttempts = 3
	var lastErr error
	for range mintAttempts {
		id, err := garden.NewBedID()
		if err != nil {
			return nil, err
		}
		bed := garden.Bed{
			ID:              id,
			ProfileID:       profileID,
			Name:            name,
			Slug:            slug,
			Note:            note,
			BedRepositories: repos,
		}
		body, err := bed.Encode()
		if err != nil {
			return nil, err
		}
		absent := docstore.ExpectAbsent
		fact := documentChangedFact(garden.Namespace, garden.CollectionBeds, bed.ID, false)
		now := d.gardenTime()
		written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
			Schema: *schema, ID: bed.ID, Body: body, Expected: &absent,
		}, fact, now)
		if err != nil {
			if docstore.IsConflict(err) {
				lastErr = err
				continue
			}
			return nil, err
		}
		d.announceCommittedWrite(fact, written.Seq)
		doc, found, readErr := d.store.GetDocument(*schema, bed.ID)
		if readErr != nil || !found {
			doc = &docstore.Document{ID: bed.ID, Body: body, Rev: written.Rev, CreatedAt: now, UpdatedAt: now}
		}
		return &protocol.SeedBedResult{Bed: d.bedToProtocol(bed, *doc), Changed: protocol.Ptr(true)}, nil
	}
	return nil, fmt.Errorf("minted %d bed ids and every one was taken: %w", mintAttempts, lastErr)
}

type bedPatch struct {
	name     *string
	note     string
	setNote  bool
	repos    []string
	setRepos bool
}

func (d *Daemon) seedBedSet(ctx context.Context, caller command.Caller, profileID, ref string, patch bedPatch) (*protocol.SeedBedResult, error) {
	if !d.on(setting.GardenV2Enabled) {
		return nil, gardenV2OffRefusal()
	}
	var repos garden.BedRepositories
	if patch.setRepos {
		bed, err := d.resolveBedInProfile(profileID, ref)
		if err != nil {
			return nil, err
		}
		ref = bed.ID
		if slices.Equal(d.bedRepoAddresses(bed.BedRepositories), patch.repos) {
			patch.setRepos = false
		} else {
			repos, err = d.resolveBedRepos(ctx, caller, patch.repos, bed.Paths)
			if err != nil {
				return nil, err
			}
		}
	}

	d.gardenBedsMu.Lock()
	defer d.gardenBedsMu.Unlock()

	existing, docs, _, err := d.bedsInProfile(profileID)
	if err != nil {
		return nil, err
	}
	current, ok := garden.FindBed(existing, ref)
	if !ok {
		_, err := d.resolveBedInProfile(profileID, ref)
		return nil, err
	}
	next := current
	if patch.name != nil {
		name, err := garden.ValidateBedName(*patch.name)
		if err != nil {
			return nil, err
		}
		next.Name = name
		next.Slug = garden.BedSlug(name)
	}
	if patch.setNote {
		note, err := garden.ValidateBedNote(patch.note)
		if err != nil {
			return nil, err
		}
		next.Note = note
	}
	if patch.setRepos {
		next.BedRepositories = repos
	}
	if next.Name == current.Name && next.Slug == current.Slug && next.Note == current.Note && sameBedRepos(next.BedRepositories, current.BedRepositories) {
		return &protocol.SeedBedResult{Bed: d.bedToProtocol(current, docs[current.ID]), Changed: protocol.Ptr(false)}, nil
	}
	var changedRepos garden.BedRepositories
	if !sameBedRepos(next.BedRepositories, current.BedRepositories) {
		changedRepos = next.BedRepositories
	}
	if err := d.validateBedUniqueness(existing, current.ID, next.Name, next.Slug, changedRepos); err != nil {
		return nil, err
	}
	schema, err := d.bedsCollection()
	if err != nil {
		return nil, err
	}
	body, err := next.Encode()
	if err != nil {
		return nil, err
	}
	expected := docs[current.ID].Rev
	fact := documentChangedFact(garden.Namespace, garden.CollectionBeds, next.ID, false)
	now := d.gardenTime()
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: *schema, ID: next.ID, Body: body, Expected: &expected,
	}, fact, now)
	if err != nil {
		return nil, err
	}
	d.announceCommittedWrite(fact, written.Seq)
	doc, found, readErr := d.store.GetDocument(*schema, next.ID)
	if readErr != nil || !found {
		doc = &docstore.Document{ID: next.ID, Body: body, Rev: written.Rev, CreatedAt: docs[current.ID].CreatedAt, UpdatedAt: now}
	}
	return &protocol.SeedBedResult{Bed: d.bedToProtocol(next, *doc), Changed: protocol.Ptr(true)}, nil
}

func (d *Daemon) seedBedRemove(profileID, ref string) (*protocol.SeedBedDeleteResult, error) {
	if !d.on(setting.GardenV2Enabled) {
		return nil, gardenV2OffRefusal()
	}

	d.gardenBedsMu.Lock()
	defer d.gardenBedsMu.Unlock()

	existing, docs, _, err := d.bedsInProfile(profileID)
	if err != nil {
		return nil, err
	}
	target, ok := garden.FindBed(existing, ref)
	if !ok {
		_, err := d.resolveBedInProfile(profileID, ref)
		return nil, err
	}
	bedSchema, err := d.bedsCollection()
	if err != nil {
		return nil, err
	}
	seedSchema, err := d.seedsCollection()
	if err != nil {
		return nil, err
	}

	var unassigned int
	var removeErr error
	d.coalesceSnapshots(func() {
		expected := docs[target.ID].Rev
		fact := documentChangedFact(garden.Namespace, garden.CollectionBeds, target.ID, true)
		written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
			Schema: *bedSchema, ID: target.ID, Delete: true, Expected: &expected,
		}, fact, d.gardenTime())
		if err != nil {
			removeErr = err
			return
		}
		if written.Changed {
			d.announceCommittedWrite(fact, written.Seq)
		}
		allSeeds, err := d.rememberedGardenSeeds()
		if err != nil {
			removeErr = err
			return
		}
		for _, candidate := range allSeeds.inProfile(profileID).seeds {
			if candidate.BedID != target.ID {
				continue
			}
			const attempts = 3
			for range attempts {
				current, doc, readErr := d.readSeed(candidate.ID)
				if readErr != nil || current.BedID != target.ID {
					break
				}
				next := current
				next.BedID = ""
				if _, _, writeErr := d.writeSeedChange(*seedSchema, next, doc.Rev, seedChange{author: who.User()}); writeErr != nil {
					if docstore.IsConflict(writeErr) {
						continue
					}
					d.logf("garden: clearing bed %s from seed %s: %v", target.ID, candidate.ID, writeErr)
					break
				}
				unassigned++
				break
			}
		}
	})
	if removeErr != nil {
		return nil, removeErr
	}
	return &protocol.SeedBedDeleteResult{
		ID:              target.ID,
		Name:            target.Name,
		UnassignedSeeds: unassigned,
	}, nil
}

func (d *Daemon) checkSeedStandingRefusal(seed garden.Seed, setter who.Actor, action string) error {
	if err := d.checkSeedStanding(seed, setter, action); err != nil {
		return protocol.Refuse(protocol.RefusalNotTender, "%s", err.Error())
	}
	return nil
}

func (d *Daemon) seedBedAssign(profileID, seedID, bedRef string, clear bool, setter who.Actor, cause protocol.SessionID) (*protocol.SeedBedAssignResult, error) {
	if !d.on(setting.GardenV2Enabled) {
		return nil, gardenV2OffRefusal()
	}
	targetBedID := ""
	if !clear {
		bed, err := d.resolveBedInProfile(profileID, bedRef)
		if err != nil {
			return nil, err
		}
		targetBedID = bed.ID
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return nil, err
	}
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(seedID)
		if err != nil {
			return nil, err
		}
		if _, hasParent := garden.ParentOf(seed); hasParent {
			read, readErr := d.readGarden(seed.ProfileID)
			rootID := seed.ID
			if readErr == nil {
				cur := seed
				seen := map[string]bool{cur.ID: true}
				for {
					parentID, ok := garden.ParentOf(cur)
					if !ok || seen[parentID] {
						break
					}
					parent, found := read.plots.Seed(parentID)
					if !found {
						rootID = parentID
						break
					}
					seen[parentID] = true
					cur = parent
					rootID = cur.ID
				}
			}
			return nil, protocol.Refuse(protocol.RefusalInvalidInput,
				"%s is part of %s and inherits its root plot's bed; assign the root plot instead: {attn seed bed assign %s <bed>}", seed.ID, rootID, rootID)
		}
		if err := d.checkSeedStandingRefusal(seed, setter, "change its bed"); err != nil {
			return nil, err
		}
		if seed.BedID == targetBedID {
			return &protocol.SeedBedAssignResult{Seed: d.seedWire(seed, doc), Changed: false}, nil
		}
		next := seed
		next.BedID = targetBedID
		written, _, err := d.writeSeedChange(*schema, next, doc.Rev, seedChange{author: setter, cause: cause})
		if docstore.IsConflict(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &protocol.SeedBedAssignResult{Seed: d.seedWire(next, written), Changed: true}, nil
	}
	return nil, protocol.Refuse(protocol.RefusalConflict, "%s was rewritten under all %d attempts to change its bed; try again", seedID, attempts)
}

func (d *Daemon) seedPulse(seedID, rawText string, clear bool, setter who.Actor, cause protocol.SessionID) (*protocol.SeedPulseResult, error) {
	if !d.on(setting.GardenV2Enabled) {
		return nil, gardenV2OffRefusal()
	}
	text := ""
	if !clear {
		var err error
		if text, err = garden.ValidatePulse(rawText); err != nil {
			return nil, err
		}
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return nil, err
	}
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(seedID)
		if err != nil {
			return nil, err
		}
		if garden.Closed(seed.Status) {
			return nil, protocol.Refuse(protocol.RefusalWrongState,
				"%s is %s; reopen it with {attn seed replant %s} before updating its pulse", seed.ID, seed.Status, seed.ID)
		}
		if err := d.checkSeedStandingRefusal(seed, setter, "update its pulse"); err != nil {
			return nil, err
		}
		if clear {
			if seed.Pulse == "" && seed.PulseAt == "" {
				return &protocol.SeedPulseResult{Seed: d.seedWire(seed, doc), Changed: false}, nil
			}
		} else if seed.Pulse == text {
			return &protocol.SeedPulseResult{Seed: d.seedWire(seed, doc), Changed: false}, nil
		}
		next := seed
		if clear {
			next.Pulse = ""
			next.PulseAt = ""
		} else {
			next.Pulse = text
			next.PulseAt = formatGardenTime(d.gardenTime())
		}
		written, _, err := d.writeSeedChange(*schema, next, doc.Rev, seedChange{author: setter, cause: cause})
		if docstore.IsConflict(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &protocol.SeedPulseResult{Seed: d.seedWire(next, written), Changed: true}, nil
	}
	return nil, protocol.Refuse(protocol.RefusalConflict, "%s was rewritten under all %d attempts to update its pulse; try again", seedID, attempts)
}

func (d *Daemon) resolveBedForPlanting(ctx context.Context, profileID, requestedBed, partOf string, sourceSession protocol.SessionID, callerDir string) (string, error) {
	requestedBed = strings.TrimSpace(requestedBed)
	if requestedBed != "" && !d.on(setting.GardenV2Enabled) {
		return "", gardenV2OffRefusal()
	}
	if requestedBed != "" && partOf != "" {
		return "", protocol.Refuse(protocol.RefusalInvalidInput,
			"a child seed inherits its root plot's bed; omit the bed here or assign the root plot with {attn seed bed assign %s <bed>}", partOf)
	}
	if !d.on(setting.GardenV2Enabled) || partOf != "" {
		return "", nil
	}
	if requestedBed != "" {
		bed, err := d.resolveBedInProfile(profileID, requestedBed)
		if err != nil {
			return "", err
		}
		return bed.ID, nil
	}
	beds, _, _, err := d.bedsInProfile(profileID)
	if err != nil || len(beds) == 0 {
		return "", nil
	}
	repo := d.callerRepoForBedRouting(ctx, sourceSession, callerDir)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if bed, ok := d.matchBedForRepo(beds, repo); ok {
		return bed.ID, nil
	}
	return "", nil
}

func (d *Daemon) runSeedBedLs(_ context.Context, c command.Caller, _ command.SeedBedLsArgs) (any, error) {
	msg := seedBedScopeRequest{Cmd: cmdSeedBed}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	result, err := d.seedBedList(protocol.Deref(msg.ProfileID))
	if err != nil {
		return nil, gardenFailure("bed ls", err)
	}
	return result, nil
}

func (d *Daemon) runSeedBedAdd(ctx context.Context, c command.Caller, args command.SeedBedAddArgs) (any, error) {
	msg := seedBedScopeRequest{Cmd: cmdSeedBed}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	result, err := d.seedBedAdd(ctx, c, protocol.Deref(msg.ProfileID), args.Name, args.Note, args.Repos)
	if err != nil {
		return nil, gardenFailure("bed add", err)
	}
	return result, nil
}

func (d *Daemon) runSeedBedSet(ctx context.Context, c command.Caller, args command.SeedBedSetArgs) (any, error) {
	if args.ClearNote && strings.TrimSpace(args.Note) != "" {
		return nil, command.Refuse(command.CodeNoteAndClear, "pass {note} or {clear_note}, not both")
	}
	if args.ClearRepos && len(args.Repos) > 0 {
		return nil, command.Refuse(command.CodeReposAndClear, "pass {repos} or {clear_repos}, not both")
	}
	hasName := strings.TrimSpace(args.Name) != ""
	hasNote := args.ClearNote || strings.TrimSpace(args.Note) != ""
	hasRepos := args.ClearRepos || len(args.Repos) > 0
	if !hasName && !hasNote && !hasRepos {
		return nil, command.Refuse(command.CodeNothingToChange, "pass at least one of {name}, {note}, {clear_note}, {repos} or {clear_repos}")
	}
	msg := seedBedScopeRequest{Cmd: cmdSeedBed}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	patch := bedPatch{
		setNote:  hasNote,
		setRepos: hasRepos,
	}
	if hasName {
		patch.name = protocol.Ptr(args.Name)
	}
	if !args.ClearNote && hasNote {
		patch.note = args.Note
	}
	if !args.ClearRepos && hasRepos {
		patch.repos = args.Repos
	}
	result, err := d.seedBedSet(ctx, c, protocol.Deref(msg.ProfileID), args.Bed, patch)
	if err != nil {
		return nil, gardenFailure("bed set", err)
	}
	return result, nil
}

func (d *Daemon) runSeedBedRm(_ context.Context, c command.Caller, args command.SeedBedRmArgs) (any, error) {
	msg := seedBedScopeRequest{Cmd: cmdSeedBed}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	result, err := d.seedBedRemove(protocol.Deref(msg.ProfileID), args.Bed)
	if err != nil {
		return nil, gardenFailure("bed rm", err)
	}
	return result, nil
}

func (d *Daemon) runSeedBedAssign(_ context.Context, c command.Caller, args command.SeedBedAssignArgs) (any, error) {
	bed := strings.TrimSpace(args.Bed)
	if bed != "" && args.Clear {
		return nil, command.Refuse(command.CodeBedAndClear, "pass a bed or {clear}, not both")
	}
	if bed == "" && !args.Clear {
		return nil, command.Refuse(command.CodeNeedsBed, "pass an existing bed or {clear}")
	}
	msg := seedBedScopeRequest{Cmd: cmdSeedBed, SeedID: args.Seed}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	result, err := d.seedBedAssign(protocol.Deref(msg.ProfileID), msg.SeedID, bed, args.Clear, d.sessionOrOwner(c.Session), c.Session)
	if err != nil {
		return nil, gardenFailure("bed assign", err)
	}
	return result, nil
}

func (d *Daemon) runSeedPulse(_ context.Context, c command.Caller, args command.SeedPulseArgs) (any, error) {
	text := strings.TrimSpace(args.Text)
	if text != "" && args.Clear {
		return nil, command.Refuse(command.CodeTextAndClear, "pass {text} or {clear}, not both")
	}
	if text == "" && !args.Clear {
		return nil, command.Refuse(command.CodeNeedsPulse, "pass a one-line status in {text}, or {clear}")
	}
	msg := seedBedScopeRequest{Cmd: cmdSeedPulse, SeedID: args.Seed}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	result, err := d.seedPulse(msg.SeedID, args.Text, args.Clear, d.sessionOrOwner(c.Session), c.Session)
	if err != nil {
		return nil, gardenFailure("pulse", err)
	}
	return result, nil
}

func (d *Daemon) handleGardenBedSaveWS(client *wsClient, msg *protocol.GardenBedSaveMessage) {
	reply := protocol.GardenBedSaveResultMessage{
		Event:     protocol.EventGardenBedSaveResult,
		RequestID: msg.RequestID,
	}
	profileID := client.selectedProfile()
	if profileID == "" {
		reply.Error = protocol.Ptr("no profile selected")
		d.sendToClient(client, reply)
		return
	}
	d.life.Go("gardenBedSave", func() {
		ctx := d.life.Context()
		var (
			res *protocol.SeedBedResult
			err error
		)
		if id := strings.TrimSpace(protocol.Deref(msg.ID)); id == "" {
			res, err = d.seedBedAdd(ctx, command.Caller{}, profileID, msg.Name, protocol.Deref(msg.Note), msg.Repos)
		} else {
			res, err = d.seedBedSet(ctx, command.Caller{}, profileID, id, bedPatch{
				name:     protocol.Ptr(msg.Name),
				note:     protocol.Deref(msg.Note),
				setNote:  true,
				repos:    msg.Repos,
				setRepos: true,
			})
		}
		if err != nil {
			reply.Error = protocol.Ptr(err.Error())
		} else {
			reply.Success = true
			reply.Bed = &res.Bed
		}
		d.sendToClient(client, reply)
	})
}

func (d *Daemon) handleGardenBedDeleteWS(client *wsClient, msg *protocol.GardenBedDeleteMessage) {
	reply := protocol.GardenBedDeleteResultMessage{
		Event:     protocol.EventGardenBedDeleteResult,
		RequestID: msg.RequestID,
	}
	profileID := client.selectedProfile()
	if profileID == "" {
		reply.Error = protocol.Ptr("no profile selected")
		d.sendToClient(client, reply)
		return
	}
	res, err := d.seedBedRemove(profileID, msg.ID)
	if err != nil {
		reply.Error = protocol.Ptr(err.Error())
	} else {
		reply.Success = true
		reply.UnassignedSeeds = protocol.Ptr(res.UnassignedSeeds)
	}
	d.sendToClient(client, reply)
}

func (d *Daemon) handleSeedBedSetWS(client *wsClient, msg *protocol.SeedBedSetMessage) {
	reply := protocol.SeedBedSetResultMessage{
		Event:     protocol.EventSeedBedSetResult,
		RequestID: msg.RequestID,
	}
	bedRef := strings.TrimSpace(protocol.Deref(msg.BedID))
	res, err := d.seedBedAssign(protocol.Deref(msg.ProfileID), msg.SeedID, bedRef, bedRef == "", client.caller, "")
	if err != nil {
		reply.Error = protocol.Ptr(err.Error())
	} else {
		reply.Success = true
		reply.Seed = &res.Seed
	}
	d.sendToClient(client, reply)
}
