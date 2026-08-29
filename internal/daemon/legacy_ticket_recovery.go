package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/transcript"
)

const (
	legacyTicketRecoveryKind    = "recover_legacy_closed_work"
	legacyTicketRecoveryKey     = "version-2"
	legacyTicketRecoveryTimeout = 30 * time.Minute
)

type legacyTicketRecoveryCounts struct {
	Sources             int `json:"sources"`
	Recovered           int `json:"recovered"`
	TranscriptRecovered int `json:"transcript_recovered"`
	NotebookAttachments int `json:"notebook_attachments"`
	DelegationsSalvaged int `json:"delegations_salvaged"`
	FragmentsSalvaged   int `json:"fragments_salvaged"`
	LiveWon             int `json:"live_won"`
	Automation          int `json:"automation_excluded"`
	Ambiguous           int `json:"ambiguous"`
	Superseded          int `json:"superseded"`
	Warnings            int `json:"warnings"`
}

type legacyTicketRecoveryResult struct {
	Counts    legacyTicketRecoveryCounts `json:"counts"`
	Warnings  []string                   `json:"warnings,omitempty"`
	Protected []string                   `json:"protected,omitempty"`
	Artifacts []string                   `json:"artifacts,omitempty"`

	databaseSeen   map[string]struct{}
	automationSeen map[string]struct{}
	fragments      []legacyRecoveryFragment
}

var (
	legacyConversationIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

type legacyBackupRoot struct {
	dir    string
	family string
	match  func(string) bool
}

func (d *Daemon) legacyTicketRecoveryEligible() bool {
	if config.Profile() != "" || d.store == nil || d.store.DatabasePath() == "" || d.store.DatabasePath() == ":memory:" {
		return false
	}
	return d.requireHome(garden.Surface) == nil
}

// prepareLegacyTicketRecovery performs the only synchronous part of recovery:
// it freezes the exact backup files before any attn pruner can run.
func (d *Daemon) prepareLegacyTicketRecovery() (bool, error) {
	if !d.legacyTicketRecoveryEligible() {
		return false, nil
	}
	d.legacyTicketRecoveryNeeded = true
	if run, err := d.store.GetLegacyTicketRecoveryRun(store.LegacyTicketRecoveryVersion); err != nil {
		return false, err
	} else if run != nil {
		return !run.State.Terminal(), nil
	}

	root := d.backupDir()
	premigrationRoot := store.BackupDirForDatabase(d.store.DatabasePath())
	inventory, err := inventoryLegacyTicketBackups([]legacyBackupRoot{
		{dir: root, family: "routine", match: store.IsRotatingBackupName},
		{dir: premigrationRoot, family: "premigration", match: store.IsPremigrationBackupName},
	})
	if err != nil {
		return false, err
	}
	transcriptInventory, err := inventoryLegacyTicketTranscripts()
	if err != nil {
		return false, err
	}
	inventory = append(inventory, transcriptInventory...)
	run, _, err := d.store.BeginLegacyTicketRecovery(store.LegacyTicketRecoveryVersion, inventory, time.Now())
	if err != nil {
		return false, err
	}
	return run != nil && !run.State.Terminal(), nil
}

func inventoryLegacyTicketTranscripts() ([]store.LegacyTicketRecoverySource, error) {
	roots, err := transcript.ResolveLegacyRecoveryRoots()
	if err != nil {
		return nil, err
	}
	sources, err := transcript.EnumerateLegacyRecoverySources(roots)
	if err != nil {
		return nil, err
	}
	out := make([]store.LegacyTicketRecoverySource, 0, len(sources))
	for _, source := range sources {
		identity, err := readLegacySnapshotIdentity(source.Path)
		if err != nil {
			return nil, fmt.Errorf("inventory %s transcript %s: %w", source.Provider, source.Path, err)
		}
		identity.RunVersion = store.LegacyTicketRecoveryVersion
		identity.Family = "transcript:" + source.Provider
		out = append(out, identity)
	}
	return out, nil
}

func inventoryLegacyTicketBackups(roots []legacyBackupRoot) ([]store.LegacyTicketRecoverySource, error) {
	byPath := make(map[string]store.LegacyTicketRecoverySource)
	for _, root := range roots {
		absRoot, err := filepath.Abs(root.dir)
		if err != nil {
			return nil, fmt.Errorf("resolve backup root %s: %w", root.dir, err)
		}
		entries, err := os.ReadDir(absRoot)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inventory backup root %s: %w", absRoot, err)
		}
		for _, entry := range entries {
			if !root.match(entry.Name()) {
				continue
			}
			path := filepath.Clean(filepath.Join(absRoot, entry.Name()))
			info, err := os.Lstat(path)
			if err != nil {
				return nil, fmt.Errorf("inventory backup %s: %w", path, err)
			}
			if !info.Mode().IsRegular() {
				continue
			}
			identity, err := readLegacySnapshotIdentity(path)
			if err != nil {
				return nil, fmt.Errorf("inventory backup %s: %w", path, err)
			}
			identity.Family = root.family
			identity.RunVersion = store.LegacyTicketRecoveryVersion
			if prior, ok := byPath[path]; ok {
				if prior.Size != identity.Size || prior.ModTimeNS != identity.ModTimeNS || prior.SHA256 != identity.SHA256 {
					return nil, fmt.Errorf("backup %s changed while de-duplicating its roots", path)
				}
				continue
			}
			byPath[path] = identity
		}
	}
	out := make([]store.LegacyTicketRecoverySource, 0, len(byPath))
	for _, source := range byPath {
		out = append(out, source)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func readLegacySnapshotIdentity(path string) (store.LegacyTicketRecoverySource, error) {
	var source store.LegacyTicketRecoverySource
	before, err := os.Lstat(path)
	if err != nil {
		return source, err
	}
	if !before.Mode().IsRegular() {
		return source, fmt.Errorf("not a direct regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return source, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return source, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return source, fmt.Errorf("file identity changed before hashing")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return source, err
	}
	afterFD, err := f.Stat()
	if err != nil {
		return source, err
	}
	afterPath, err := os.Lstat(path)
	if err != nil {
		return source, err
	}
	if !afterPath.Mode().IsRegular() || !os.SameFile(before, afterFD) || !os.SameFile(before, afterPath) ||
		before.Size() != afterFD.Size() || before.ModTime() != afterFD.ModTime() {
		return source, fmt.Errorf("file identity changed while hashing")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return source, err
	}
	source.Path = filepath.Clean(abs)
	source.Size = before.Size()
	source.ModTimeNS = before.ModTime().UnixNano()
	source.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return source, nil
}

func legacySnapshotIdentityMatches(frozen, current store.LegacyTicketRecoverySource) bool {
	return frozen.Path == current.Path && frozen.Size == current.Size &&
		frozen.ModTimeNS == current.ModTimeNS && frozen.SHA256 == current.SHA256
}

func (d *Daemon) readLegacyTicketSnapshotIdentity(path string) (store.LegacyTicketRecoverySource, error) {
	if d.legacyTicketSnapshotIdentity != nil {
		return d.legacyTicketSnapshotIdentity(path)
	}
	return readLegacySnapshotIdentity(path)
}

func (d *Daemon) readLegacyTicketSnapshot(path string) (store.LegacyTicketSnapshotRead, error) {
	if d.legacyTicketSnapshotRead != nil {
		return d.legacyTicketSnapshotRead(path)
	}
	return store.ReadLegacyTicketSnapshot(path)
}

func (d *Daemon) enqueueLegacyTicketRecovery() error {
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return errors.New("durable job queue unavailable")
	}
	existing, err := runner.GetByKey(legacyTicketRecoveryKind, legacyTicketRecoveryKey)
	if err != nil {
		return err
	}
	if existing != nil {
		if existing.State == jobs.StateDead {
			d.finalizeExhaustedLegacyTicketRecovery(existing)
		}
		return nil
	}
	_, err = runner.Enqueue(legacyTicketRecoveryKind, jobs.EnqueueOptions{
		UniqueKey:   legacyTicketRecoveryKey,
		RunNow:      true,
		Priority:    100,
		MaxAttempts: 3,
	})
	return err
}

func (d *Daemon) legacyTicketRecoveryHandler(ctx context.Context, job *jobs.Job) (any, error) {
	d.ensureGardenCollections()
	run, err := d.store.GetLegacyTicketRecoveryRun(store.LegacyTicketRecoveryVersion)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, errors.New("legacy ticket recovery run is missing")
	}
	if run.State.Terminal() {
		d.startPostLegacyTicketRecovery()
		return legacyTicketRecoveryResult{}, nil
	}

	result, transientErr := d.recoverLegacyTicketsFromSnapshots(ctx, job, run)
	if transientErr == nil {
		transientErr = d.recoverLegacyTicketsFromTranscripts(ctx, job, run, &result)
	}
	if transientErr == nil {
		transientErr = d.recoverLegacyTicketNotebook(ctx, job, run, &result)
	}
	if transientErr == nil {
		transientErr = d.recoverLegacyTicketSeeds(ctx, job, run, &result)
	}
	if transientErr == nil {
		transientErr = d.salvageLegacyRecoveryEvidence(ctx, run, &result)
	}
	if transientErr != nil && job.Attempts < 3 {
		return nil, transientErr
	}
	terminalError := ""
	if transientErr != nil {
		terminalError = transientErr.Error()
		result.Warnings = append(result.Warnings, "recovery exhausted its transient I/O retries: "+terminalError)
	}
	result.Warnings = uniqueStrings(result.Warnings)
	sort.Strings(result.Warnings)
	result.Artifacts = uniqueStrings(result.Artifacts)
	sort.Strings(result.Artifacts)
	result.Counts.Warnings = len(result.Warnings)
	protected, err := d.store.ProtectedLegacyTicketRecoveryPaths(store.LegacyTicketRecoveryVersion)
	if err != nil {
		return nil, err
	}
	for path := range protected {
		result.Protected = append(result.Protected, path)
	}
	sort.Strings(result.Protected)

	state := store.LegacyTicketRecoverySucceeded
	var warning *store.NotificationRecord
	if len(result.Warnings) > 0 {
		state = store.LegacyTicketRecoveryWarned
		warning = legacyTicketRecoveryWarning(result, terminalError)
	}
	if err := withLegacyRecoveryCommit(job, func() error {
		id, finishErr := d.store.FinishLegacyTicketRecovery(
			store.LegacyTicketRecoveryVersion, state, result.Counts, terminalError, warning, time.Now())
		if finishErr == nil && id != "" {
			d.publishFact(FactNotificationCreated, id, nil)
		}
		return finishErr
	}); err != nil {
		return nil, err
	}
	d.startPostLegacyTicketRecovery()
	return result, nil
}

func (d *Daemon) recoverLegacyTicketsFromSnapshots(ctx context.Context, job *jobs.Job, run *store.LegacyTicketRecoveryRun) (legacyTicketRecoveryResult, error) {
	result := legacyTicketRecoveryResult{
		databaseSeen:   make(map[string]struct{}),
		automationSeen: make(map[string]struct{}),
	}
	liveTickets, err := d.store.ListTickets(store.TicketListFilter{IncludeArchived: true})
	if err != nil {
		return result, err
	}
	for _, ticket := range liveTickets {
		if ticket != nil {
			result.databaseSeen[ticket.ID] = struct{}{}
		}
	}
	sources, err := d.store.ListLegacyTicketRecoverySources(run.Version)
	if err != nil {
		return result, err
	}
	result.Counts.Sources = len(sources)
	candidatesByTicket := make(map[string][]store.LegacyTicketCandidate)
	sourceWarnings := make(map[string][]string)

	for _, source := range sources {
		if source.Family != "routine" && source.Family != "premigration" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		before, err := d.readLegacyTicketSnapshotIdentity(source.Path)
		if err != nil {
			if isTransientLegacyRecoveryError(err) {
				return result, fmt.Errorf("read frozen source %s: %w", source.Path, err)
			}
			sourceWarnings[source.Path] = append(sourceWarnings[source.Path], err.Error())
			continue
		}
		if !legacySnapshotIdentityMatches(source, before) {
			sourceWarnings[source.Path] = append(sourceWarnings[source.Path], "source identity changed before inspection")
			continue
		}
		read, err := d.readLegacyTicketSnapshot(source.Path)
		if err != nil {
			if isTransientLegacyRecoveryError(err) {
				return result, fmt.Errorf("inspect frozen source %s: %w", source.Path, err)
			}
			sourceWarnings[source.Path] = append(sourceWarnings[source.Path], err.Error())
			continue
		}
		after, err := d.readLegacyTicketSnapshotIdentity(source.Path)
		if err != nil {
			if isTransientLegacyRecoveryError(err) {
				return result, fmt.Errorf("verify frozen source %s: %w", source.Path, err)
			}
			sourceWarnings[source.Path] = append(sourceWarnings[source.Path], err.Error())
			continue
		}
		if !legacySnapshotIdentityMatches(source, after) {
			sourceWarnings[source.Path] = append(sourceWarnings[source.Path], "source identity changed during inspection")
			continue
		}
		sourceWarnings[source.Path] = append(sourceWarnings[source.Path], read.Warnings...)
		for _, id := range read.TicketIDs {
			result.databaseSeen[id] = struct{}{}
		}
		for _, id := range read.AutomationTicketIDs {
			result.automationSeen[id] = struct{}{}
		}
		for _, candidate := range read.Candidates {
			candidatesByTicket[candidate.Ticket.ID] = append(candidatesByTicket[candidate.Ticket.ID], candidate)
		}
	}

	ids := make([]string, 0, len(candidatesByTicket))
	for id := range candidatesByTicket {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, ticketID := range ids {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		candidates := candidatesByTicket[ticketID]
		automationOwned := false
		for _, candidate := range candidates {
			automationOwned = automationOwned || candidate.AutomationOwned
		}
		if automationOwned {
			for _, candidate := range uniqueLegacyCandidates(candidates) {
				item := legacyTicketItem(run, candidate, "automation_excluded", "positive Automation-feature provenance")
				if err := withLegacyRecoveryCommit(job, func() error {
					_, recordErr := d.store.RecordLegacyTicketRecoveryItem(item)
					return recordErr
				}); err != nil {
					return result, err
				}
			}
			result.Counts.Automation++
			continue
		}

		newest := newestLegacyCandidates(candidates)
		newestUnique := uniqueLegacyCandidates(newest)
		if len(newestUnique) != 1 {
			result.Counts.Ambiguous++
			message := fmt.Sprintf("ticket %s has %d disagreeing revisions at %s", ticketID, len(newestUnique), newest[0].Ticket.UpdatedAt.UTC().Format(time.RFC3339))
			result.Warnings = append(result.Warnings, message)
			for _, candidate := range newestUnique {
				payload, _ := json.Marshal(candidate)
				result.fragments = append(result.fragments, legacyRecoveryFragment{
					Kind: "database_ticket_ambiguity", TicketID: ticketID,
					SourcePath: candidate.SourcePath, Detail: message, Payload: payload,
				})
				sourceWarnings[candidate.SourcePath] = append(sourceWarnings[candidate.SourcePath], message)
				item := legacyTicketItem(run, candidate, "ambiguous", message)
				if err := withLegacyRecoveryCommit(job, func() error {
					_, recordErr := d.store.RecordLegacyTicketRecoveryItem(item)
					return recordErr
				}); err != nil {
					return result, err
				}
			}
			continue
		}
		chosen := newestUnique[0]
		for _, candidate := range uniqueLegacyCandidates(candidates) {
			if candidate.Fingerprint == chosen.Fingerprint {
				continue
			}
			item := legacyTicketItem(run, candidate, "superseded", "a newer compatible snapshot revision won")
			if err := withLegacyRecoveryCommit(job, func() error {
				_, recordErr := d.store.RecordLegacyTicketRecoveryItem(item)
				return recordErr
			}); err != nil {
				return result, err
			}
			result.Counts.Superseded++
		}
		item := legacyTicketItem(run, chosen, "", "")
		var restoreResult string
		if err := withLegacyRecoveryCommit(job, func() error {
			var restoreErr error
			restoreResult, restoreErr = d.store.RestoreLegacyTicket(chosen, item)
			return restoreErr
		}); err != nil {
			return result, err
		}
		switch restoreResult {
		case "recovered":
			result.Counts.Recovered++
		case "live_won":
			result.Counts.LiveWon++
		}
	}

	for _, source := range sources {
		if source.Family != "routine" && source.Family != "premigration" {
			continue
		}
		state, detail := "complete", ""
		if warnings := sourceWarnings[source.Path]; len(warnings) > 0 {
			state = "protected"
			detail = strings.Join(warnings, "; ")
			for _, warning := range warnings {
				result.Warnings = append(result.Warnings, source.Path+": "+warning)
			}
		}
		if err := withLegacyRecoveryCommit(job, func() error {
			return d.store.SetLegacyTicketRecoverySourceState(run.Version, source.Path, state, detail)
		}); err != nil {
			return result, err
		}
	}
	return result, nil
}

type legacyTranscriptEvidence struct {
	inspection transcript.LegacyTranscriptInspection
	source     store.LegacyTicketRecoverySource
}

func (d *Daemon) recoverLegacyTicketsFromTranscripts(ctx context.Context, job *jobs.Job, run *store.LegacyTicketRecoveryRun, result *legacyTicketRecoveryResult) error {
	sources, err := d.store.ListLegacyTicketRecoverySources(run.Version)
	if err != nil {
		return err
	}
	if result.databaseSeen == nil {
		result.databaseSeen = make(map[string]struct{})
	}
	if result.automationSeen == nil {
		result.automationSeen = make(map[string]struct{})
	}

	byTicket := make(map[string][]legacyTranscriptEvidence)
	bound := make(map[string][]legacyTranscriptEvidence)
	sourceWarnings := make(map[string][]string)
	for _, source := range sources {
		if !strings.HasPrefix(source.Family, "transcript:") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		before, err := d.readLegacyTicketSnapshotIdentity(source.Path)
		if err != nil {
			if isTransientLegacyRecoveryError(err) {
				return fmt.Errorf("read frozen transcript %s: %w", source.Path, err)
			}
			sourceWarnings[source.Path] = append(sourceWarnings[source.Path], err.Error())
			continue
		}
		if !legacySnapshotIdentityMatches(source, before) {
			sourceWarnings[source.Path] = append(sourceWarnings[source.Path], "source identity changed before inspection")
			continue
		}
		provider := strings.TrimPrefix(source.Family, "transcript:")
		transcriptSource := transcript.LegacyRecoverySourceAt(provider, source.Path)
		if transcriptSource.NativeSessionID == "" {
			sourceWarnings[source.Path] = append(sourceWarnings[source.Path], "native conversation identity is unavailable")
			continue
		}
		inspection, err := transcript.InspectLegacyRecoveryTranscript(transcriptSource, d.dataRoot)
		if err != nil {
			if isTransientLegacyRecoveryError(err) {
				return fmt.Errorf("inspect frozen transcript %s: %w", source.Path, err)
			}
			detail := err.Error()
			sourceWarnings[source.Path] = append(sourceWarnings[source.Path], detail)
			result.fragments = append(result.fragments, legacyRecoveryFragment{
				Kind: "transcript_inspection_error", SourcePath: source.Path, Detail: detail,
				Transcript: &legacyTranscriptFragment{
					Provider: provider, NativeSessionID: transcriptSource.NativeSessionID,
				},
			})
			continue
		}
		after, err := d.readLegacyTicketSnapshotIdentity(source.Path)
		if err != nil {
			if isTransientLegacyRecoveryError(err) {
				return fmt.Errorf("verify frozen transcript %s: %w", source.Path, err)
			}
			sourceWarnings[source.Path] = append(sourceWarnings[source.Path], err.Error())
			continue
		}
		if !legacySnapshotIdentityMatches(before, after) {
			sourceWarnings[source.Path] = append(sourceWarnings[source.Path], "source identity changed during inspection")
			continue
		}
		for _, warning := range inspection.Warnings {
			sourceWarnings[source.Path] = append(sourceWarnings[source.Path], warning)
			result.fragments = append(result.fragments, legacyRecoveryFragment{
				Kind: "transcript_warning", SourcePath: source.Path, Detail: warning,
				Transcript: &legacyTranscriptFragment{
					Provider: provider, NativeSessionID: transcriptSource.NativeSessionID,
					Production: inspection.Production,
				},
			})
		}
		evidence := legacyTranscriptEvidence{inspection: inspection, source: source}
		for _, receipt := range inspection.Receipts {
			byTicket[receipt.TicketID] = append(byTicket[receipt.TicketID], evidence)
			if receipt.Bound && inspection.Production {
				bound[receipt.TicketID] = appendUniqueTranscriptEvidence(bound[receipt.TicketID], evidence)
			}
		}
	}
	for ticketID, evidence := range byTicket {
		if _, seen := result.databaseSeen[ticketID]; seen {
			continue
		}
		seen := make(map[string]struct{})
		for _, item := range evidence {
			for _, receipt := range item.inspection.Receipts {
				if receipt.TicketID != ticketID || (item.inspection.Production && receipt.Bound &&
					(receipt.State == "done" || receipt.State == "failed" || receipt.State == "crashed")) {
					continue
				}
				if _, exists := seen[receipt.Fingerprint]; exists {
					continue
				}
				seen[receipt.Fingerprint] = struct{}{}
				result.fragments = append(result.fragments, legacyRecoveryFragment{
					Kind: "transcript_partial_receipt", TicketID: ticketID, SourcePath: item.source.Path,
					Transcript: &legacyTranscriptFragment{
						Provider: item.inspection.Source.Provider, NativeSessionID: item.inspection.Source.NativeSessionID,
						State: receipt.State, Timestamp: receipt.Timestamp.UTC().Format(time.RFC3339Nano),
						Bound: receipt.Bound, Production: item.inspection.Production,
						Explicit: receipt.Explicit, Fingerprint: receipt.Fingerprint,
					},
				})
			}
		}
	}

	ids := make([]string, 0, len(bound))
	for id := range bound {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, ticketID := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, seen := result.databaseSeen[ticketID]; seen {
			continue
		}
		live, err := d.store.GetTicket(ticketID)
		if err != nil {
			return err
		}
		if live != nil {
			continue
		}
		automation, err := d.store.HasAutomationTicketProvenance(ticketID)
		if err != nil {
			return err
		}
		_, historicalAutomation := result.automationSeen[ticketID]
		if automation || historicalAutomation {
			result.Counts.Automation++
			continue
		}

		terminal := terminalLegacyReceipts(ticketID, byTicket[ticketID], true)
		if len(terminal) == 0 {
			continue
		}
		newest := terminal[len(terminal)-1]
		statesAtNewest := map[string]struct{}{newest.State: {}}
		for i := len(terminal) - 2; i >= 0 && terminal[i].Timestamp.Equal(newest.Timestamp); i-- {
			statesAtNewest[terminal[i].State] = struct{}{}
		}
		if len(statesAtNewest) != 1 || newest.Timestamp.IsZero() {
			result.Counts.Ambiguous++
			message := fmt.Sprintf("transcript-backed ticket %s has an ambiguous newest terminal receipt", ticketID)
			result.Warnings = append(result.Warnings, message)
			for _, evidence := range byTicket[ticketID] {
				sourceWarnings[evidence.source.Path] = append(sourceWarnings[evidence.source.Path], message)
			}
			result.fragments = append(result.fragments, legacyRecoveryFragment{
				Kind: "transcript_terminal_ambiguity", TicketID: ticketID, Detail: message,
			})
			continue
		}

		boundEvidence := bound[ticketID]
		sort.Slice(boundEvidence, func(i, j int) bool {
			return boundEvidence[i].inspection.Source.Path < boundEvidence[j].inspection.Source.Path
		})
		createdAt := earliestBoundReceipt(ticketID, boundEvidence)
		if createdAt.IsZero() {
			createdAt = newest.Timestamp
		}
		var attachments []store.TicketAttachment
		var conversationPaths []string
		for _, evidence := range boundEvidence {
			if strings.TrimSpace(evidence.inspection.Conversation) == "" {
				continue
			}
			path, writeErr := d.writeLegacyConversation(evidence.inspection)
			if writeErr != nil {
				message := fmt.Sprintf("conversation %s: %v", evidence.inspection.Source.NativeSessionID, writeErr)
				result.Warnings = append(result.Warnings, message)
				sourceWarnings[evidence.source.Path] = append(sourceWarnings[evidence.source.Path], message)
				continue
			}
			conversationPaths = append(conversationPaths, path)
			attachments = append(attachments, store.TicketAttachment{
				TicketID: ticketID, Filename: filepath.Base(path), Path: path,
				Note: "Recovered human and assistant conversation", CreatedAt: run.RecoveryAt,
			})
		}

		status := store.TicketStatus(newest.State)
		closedAt := newest.Timestamp.UTC()
		archivedAt := run.RecoveryAt.UTC()
		candidate := store.LegacyTicketCandidate{
			Ticket: store.Ticket{
				ID: ticketID, Title: "Recovered ticket " + ticketID,
				Description: firstRecoveredHuman(boundEvidence), Status: status,
				Cwd: unambiguousRecoveredCWD(boundEvidence), LastAgentID: unambiguousRecoveredProvider(boundEvidence),
				CreatedAt: createdAt.UTC(), UpdatedAt: closedAt, ClosedAt: &closedAt, ArchivedAt: &archivedAt,
			},
			ResumeSessionID: unambiguousRecoveredSession(boundEvidence),
			Activity: []store.TicketActivity{{
				TicketID: ticketID, Kind: store.TicketActivityStatusChange, Author: store.TicketAuthorAttn,
				ToStatus: status, Comment: "Recovered from a proven legacy transcript.", CreatedAt: closedAt,
			}},
			Attachments: attachments,
		}
		fingerprint := legacyTranscriptCandidateFingerprint(candidate, newest, conversationPaths)
		item := store.LegacyTicketRecoveryItem{
			Fingerprint: fingerprint, RunVersion: run.Version, SourceKind: "transcript",
			SourceKey: newest.Transcript.Path, TicketID: ticketID, CreatedAt: run.RecoveryAt,
		}
		var restoreResult string
		if err := withLegacyRecoveryCommit(job, func() error {
			var restoreErr error
			restoreResult, restoreErr = d.store.RestoreLegacyTicket(candidate, item)
			return restoreErr
		}); err != nil {
			return err
		}
		switch restoreResult {
		case "recovered":
			result.Counts.Recovered++
			result.Counts.TranscriptRecovered++
		case "live_won":
			result.Counts.LiveWon++
		}

		for _, receipt := range terminalLegacyReceipts(ticketID, byTicket[ticketID], false) {
			if receipt.Timestamp.After(newest.Timestamp) && receipt.State != newest.State {
				message := fmt.Sprintf("ticket %s has a later unanchored %s receipt in %s; the production-proven %s state won",
					ticketID, receipt.State, receipt.Transcript.NativeSessionID, newest.State)
				result.Warnings = append(result.Warnings, message)
				sourceWarnings[receipt.Transcript.Path] = append(sourceWarnings[receipt.Transcript.Path], message)
				result.fragments = append(result.fragments, legacyRecoveryFragment{
					Kind: "transcript_state_conflict", TicketID: ticketID,
					SourcePath: receipt.Transcript.Path, Detail: message,
					Transcript: &legacyTranscriptFragment{
						Provider: receipt.Transcript.Provider, NativeSessionID: receipt.Transcript.NativeSessionID,
						State: receipt.State, Timestamp: receipt.Timestamp.UTC().Format(time.RFC3339Nano),
						Bound: receipt.Bound, Explicit: receipt.Explicit, Fingerprint: receipt.Fingerprint,
					},
				})
			}
		}
	}

	for _, source := range sources {
		if !strings.HasPrefix(source.Family, "transcript:") {
			continue
		}
		state, detail := "complete", ""
		if warnings := uniqueStrings(sourceWarnings[source.Path]); len(warnings) > 0 {
			state = "protected"
			detail = strings.Join(warnings, "; ")
			for _, warning := range warnings {
				result.Warnings = append(result.Warnings, source.Path+": "+warning)
			}
		}
		if err := withLegacyRecoveryCommit(job, func() error {
			return d.store.SetLegacyTicketRecoverySourceState(run.Version, source.Path, state, detail)
		}); err != nil {
			return err
		}
	}
	return nil
}

func appendUniqueTranscriptEvidence(existing []legacyTranscriptEvidence, candidate legacyTranscriptEvidence) []legacyTranscriptEvidence {
	for _, item := range existing {
		if item.source.Path == candidate.source.Path {
			return existing
		}
	}
	return append(existing, candidate)
}

func terminalLegacyReceipts(ticketID string, evidence []legacyTranscriptEvidence, production bool) []transcript.LegacyTicketReceipt {
	seen := make(map[string]struct{})
	var out []transcript.LegacyTicketReceipt
	for _, item := range evidence {
		if item.inspection.Production != production {
			continue
		}
		for _, receipt := range item.inspection.Receipts {
			if receipt.TicketID != ticketID || (receipt.State != "done" && receipt.State != "failed" && receipt.State != "crashed") {
				continue
			}
			if _, ok := seen[receipt.Fingerprint]; ok {
				continue
			}
			seen[receipt.Fingerprint] = struct{}{}
			out = append(out, receipt)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Timestamp.Equal(out[j].Timestamp) {
			return out[i].Fingerprint < out[j].Fingerprint
		}
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out
}

func earliestBoundReceipt(ticketID string, evidence []legacyTranscriptEvidence) time.Time {
	var earliest time.Time
	for _, item := range evidence {
		for _, receipt := range item.inspection.Receipts {
			if receipt.TicketID != ticketID || !receipt.Bound || receipt.Timestamp.IsZero() {
				continue
			}
			if earliest.IsZero() || receipt.Timestamp.Before(earliest) {
				earliest = receipt.Timestamp
			}
		}
	}
	return earliest
}

func firstRecoveredHuman(evidence []legacyTranscriptEvidence) string {
	for _, item := range evidence {
		if text := strings.TrimSpace(item.inspection.FirstHuman); text != "" {
			return text
		}
	}
	return ""
}

func unambiguousRecoveredCWD(evidence []legacyTranscriptEvidence) string {
	return unambiguousRecoveredValue(evidence, func(item legacyTranscriptEvidence) string { return item.inspection.CWD })
}

func unambiguousRecoveredProvider(evidence []legacyTranscriptEvidence) string {
	return unambiguousRecoveredValue(evidence, func(item legacyTranscriptEvidence) string { return item.inspection.Source.Provider })
}

func unambiguousRecoveredSession(evidence []legacyTranscriptEvidence) string {
	return unambiguousRecoveredValue(evidence, func(item legacyTranscriptEvidence) string { return item.inspection.Source.NativeSessionID })
}

func unambiguousRecoveredValue(evidence []legacyTranscriptEvidence, value func(legacyTranscriptEvidence) string) string {
	result := ""
	for _, item := range evidence {
		candidate := strings.TrimSpace(value(item))
		if candidate == "" {
			continue
		}
		if result == "" {
			result = candidate
			continue
		}
		if result != candidate {
			return ""
		}
	}
	return result
}

func legacyTranscriptCandidateFingerprint(candidate store.LegacyTicketCandidate, receipt transcript.LegacyTicketReceipt, paths []string) string {
	encoded, _ := json.Marshal(struct {
		Candidate store.LegacyTicketCandidate
		Receipt   string
		Paths     []string
	}{Candidate: candidate, Receipt: receipt.Fingerprint, Paths: paths})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (d *Daemon) writeLegacyConversation(inspection transcript.LegacyTranscriptInspection) (string, error) {
	provider := inspection.Source.Provider
	native := inspection.Source.NativeSessionID
	if (provider != "codex" && provider != "claude" && provider != "copilot") || !legacyConversationIDPattern.MatchString(native) {
		return "", errors.New("unsafe provider conversation identity")
	}
	dir, err := ensureLegacyRecoveryDirectories(d.dataRoot, "legacy-ticket-recovery", "conversations", provider)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, native+".md")
	content := []byte(inspection.Conversation)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return "", errors.New("existing conversation is not an owner-only regular file")
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", readErr
		}
		if !bytes.Equal(existing, content) {
			return "", errors.New("existing conversation differs; preserved without overwrite")
		}
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	temp, err := os.CreateTemp(dir, ".conversation-*.tmp")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return "", err
	}
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return "", err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := os.Link(tempPath, path); err != nil {
		if os.IsExist(err) {
			existing, readErr := os.ReadFile(path)
			if readErr == nil && bytes.Equal(existing, content) {
				return path, nil
			}
		}
		return "", err
	}
	return path, nil
}

func ensureLegacyRecoveryDirectories(dataRoot string, names ...string) (string, error) {
	current := filepath.Clean(dataRoot)
	if err := requireLegacyDirectDirectory(current, false); err != nil {
		return "", err
	}
	for _, name := range names {
		current = filepath.Join(current, name)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o700); err != nil && !os.IsExist(err) {
				return "", err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("recovery directory %s is not an owner-only direct directory", current)
		}
	}
	return current, nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (d *Daemon) recoverLegacyTicketSeeds(ctx context.Context, job *jobs.Job, run *store.LegacyTicketRecoveryRun, result *legacyTicketRecoveryResult) error {
	seedSchema, err := d.seedsCollection()
	if err != nil {
		return err
	}
	noteSchema, err := d.notesCollection()
	if err != nil {
		return err
	}
	dispatchSchema, err := d.dispatchesCollection()
	if err != nil {
		return err
	}
	tickets, err := d.store.ListTickets(store.TicketListFilter{IncludeArchived: true})
	if err != nil {
		return err
	}
	sort.Slice(tickets, func(i, j int) bool { return tickets[i].ID < tickets[j].ID })
	for _, ticket := range tickets {
		if err := ctx.Err(); err != nil {
			return err
		}
		if ticket == nil || !ticket.Status.IsTerminal() {
			continue
		}
		automation, err := d.store.HasAutomationTicketProvenance(ticket.ID)
		if err != nil {
			return err
		}
		if automation {
			continue
		}
		title := strings.TrimSpace(ticket.Title)
		body := strings.TrimSpace(ticket.Description)
		if err := garden.ValidatePlant(title, body); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("ticket %s could not become a seed: %v", ticket.ID, err))
			continue
		}
		sourceKind, err := d.store.LegacyTicketRecoverySourceKind(ticket.ID)
		if err != nil {
			return err
		}
		fingerprint := legacyTicketSeedFingerprint(ticket, sourceKind)
		var linked store.LegacyTicketSeedResult
		for attempt := 0; attempt < 3; attempt++ {
			seedID, err := d.mintSeedID()
			if err != nil {
				return err
			}
			seed := garden.Seed{
				ID: seedID, Title: title, Body: body, StepSlug: garden.StepSlug(title),
				Edges: []garden.Edge{}, Vars: []garden.Var{},
				ResumeSessionID: strings.TrimSpace(ticket.ResumeSessionID), ResumeCwd: strings.TrimSpace(ticket.Cwd), ResumeAgent: strings.TrimSpace(ticket.LastAgentID),
			}
			if ticket.Status == store.TicketStatusDone {
				seed.Status = garden.StatusHarvested
			} else {
				seed.Status = garden.StatusWithered
			}
			seed.Reason = "recovered from legacy ticket " + ticket.ID
			seedBody, err := seed.Encode()
			if err != nil {
				return err
			}
			notes, err := d.legacyTicketSeedNotes(ticket, seedID)
			if err != nil {
				return err
			}
			spec := store.LegacyTicketSeedSpec{
				TicketID: ticket.ID, SeedID: seedID, SeedBody: seedBody,
				SeedFact:  documentChangedFact(garden.Namespace, garden.CollectionSeeds, seedID, false),
				SeedTitle: title, SeedDescription: body,
				SeedSchema: *seedSchema, NoteSchema: *noteSchema, DispatchSchema: *dispatchSchema,
				Notes: notes, SessionIDs: []string{ticket.Assignee, ticket.ResumeSessionID},
				SourceKind: sourceKind, EvidenceFingerprint: fingerprint,
				OriginalTerminalState: ticket.Status, CreatedAt: run.RecoveryAt,
			}
			if err := withLegacyRecoveryCommit(job, func() error {
				var linkErr error
				linked, linkErr = d.store.EnsureLegacyTicketSeed(spec)
				return linkErr
			}); err != nil {
				if docstore.IsConflict(err) {
					continue
				}
				return err
			}
			if linked.Result == "created" {
				d.announceLegacyTicketSeedWrites(spec, linked)
			}
			break
		}
		switch linked.Result {
		case "created":
			d.publishFact(FactGardenPlanted, linked.SeedID, nil)
			d.publishFact(FactGardenNoted, linked.SeedID, nil)
		case "ambiguous_lineage":
			result.Counts.Ambiguous++
			result.Warnings = append(result.Warnings, fmt.Sprintf("ticket %s has several machine-proven Garden seeds; none was changed", ticket.ID))
		case "ambiguous_content":
			result.Counts.Ambiguous++
			result.Warnings = append(result.Warnings, fmt.Sprintf("ticket %s matches an unlinked seed exactly; neither seed was changed", ticket.ID))
		case "":
			return fmt.Errorf("ticket %s could not mint a collision-free seed id", ticket.ID)
		}
	}
	return nil
}

func (d *Daemon) legacyTicketSeedNotes(ticket *store.Ticket, seedID string) ([]store.LegacyTicketSeedNote, error) {
	noteID, err := d.mintNoteID()
	if err != nil {
		return nil, err
	}
	note := garden.Note{
		ID: noteID, Seed: seedID, Kind: garden.NoteKindNote,
		Body: fmt.Sprintf("Recovered from legacy ticket `%s` in state `%s`. The legacy ticket remains readable with `attn ticket show %s`.", ticket.ID, ticket.Status, ticket.ID),
	}
	body, err := note.Encode()
	if err != nil {
		return nil, err
	}
	notes := []store.LegacyTicketSeedNote{{
		ID: noteID, Body: body,
		Fact: documentChangedFact(garden.Namespace, garden.CollectionNotes, noteID, false),
	}}
	conversationRoot := filepath.Clean(filepath.Join(d.dataRoot, "legacy-ticket-recovery", "conversations")) + string(os.PathSeparator)
	for _, attachment := range ticket.Attachments {
		path := filepath.Clean(attachment.Path)
		if !strings.HasPrefix(path, conversationRoot) || filepath.Ext(path) != ".md" {
			continue
		}
		artifact, err := garden.ValidateArtifact(garden.ArtifactReference{Kind: garden.ArtifactMarkdownFile, Path: path})
		if err != nil {
			return nil, err
		}
		id, err := d.mintNoteID()
		if err != nil {
			return nil, err
		}
		attached := garden.Note{
			ID: id, Seed: seedID, Kind: garden.NoteKindAttach,
			Body: "Attached recovered conversation " + filepath.Base(path), Artifact: &artifact,
		}
		encoded, err := attached.Encode()
		if err != nil {
			return nil, err
		}
		notes = append(notes, store.LegacyTicketSeedNote{
			ID: id, Body: encoded,
			Fact: documentChangedFact(garden.Namespace, garden.CollectionNotes, id, false),
		})
	}
	return notes, nil
}

func legacyTicketSeedFingerprint(ticket *store.Ticket, sourceKind string) string {
	encoded, _ := json.Marshal(struct {
		Ticket     *store.Ticket
		SourceKind string
	}{Ticket: ticket, SourceKind: sourceKind})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func legacyTicketItem(run *store.LegacyTicketRecoveryRun, candidate store.LegacyTicketCandidate, result, detail string) store.LegacyTicketRecoveryItem {
	return store.LegacyTicketRecoveryItem{
		Fingerprint: candidate.Fingerprint, RunVersion: run.Version, SourceKind: "database",
		SourceKey: candidate.SourcePath, TicketID: candidate.Ticket.ID, Result: result,
		Detail: detail, CreatedAt: run.RecoveryAt,
	}
}

func uniqueLegacyCandidates(candidates []store.LegacyTicketCandidate) []store.LegacyTicketCandidate {
	seen := make(map[string]struct{})
	var out []store.LegacyTicketCandidate
	for _, candidate := range candidates {
		if _, ok := seen[candidate.Fingerprint]; ok {
			continue
		}
		seen[candidate.Fingerprint] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func newestLegacyCandidates(candidates []store.LegacyTicketCandidate) []store.LegacyTicketCandidate {
	if len(candidates) == 0 {
		return nil
	}
	newest := candidates[0].Ticket.UpdatedAt
	for _, candidate := range candidates[1:] {
		if candidate.Ticket.UpdatedAt.After(newest) {
			newest = candidate.Ticket.UpdatedAt
		}
	}
	var out []store.LegacyTicketCandidate
	for _, candidate := range candidates {
		if candidate.Ticket.UpdatedAt.Equal(newest) {
			out = append(out, candidate)
		}
	}
	return out
}

func withLegacyRecoveryCommit(job *jobs.Job, write func() error) error {
	if job != nil && job.CommitGuard != nil {
		if !job.CommitGuard.Enter() {
			return context.Canceled
		}
		defer job.CommitGuard.Leave()
	}
	return write()
}

func isTransientLegacyRecoveryError(err error) bool {
	if err == nil {
		return false
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && !os.IsNotExist(err) && !os.IsPermission(err) {
		return true
	}
	lower := strings.ToLower(err.Error())
	for _, phrase := range []string{"database is locked", "database is busy", "resource temporarily unavailable", "interrupted", "timeout", "timed out"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func legacyTicketRecoveryWarning(result legacyTicketRecoveryResult, terminalError string) *store.NotificationRecord {
	body := fmt.Sprintf("Recovered %d closed ticket(s); %d source or identity problem(s) need attention.",
		result.Counts.Recovered, len(result.Warnings))
	if len(result.Protected) > 0 {
		body += fmt.Sprintf(" %d recovery source(s) remain protected.", len(result.Protected))
	}
	detail := strings.Join(result.Warnings, "\n")
	if len(result.Artifacts) > 0 {
		detail += "\nLocal recovery evidence:\n" + strings.Join(result.Artifacts, "\n")
	}
	if len(result.Protected) > 0 {
		detail += "\nProtected sources:\n" + strings.Join(result.Protected, "\n")
	}
	if terminalError != "" {
		detail += "\nTerminal I/O error: " + terminalError
	}
	return &store.NotificationRecord{
		Kind: "legacy_ticket_recovery_warned", Severity: store.NotificationWarning,
		Title: "Closed ticket recovery needs attention", Body: body, Detail: strings.TrimSpace(detail),
		SourceKind: "legacy_ticket_recovery", SourceID: legacyTicketRecoveryKey,
	}
}

func (d *Daemon) finalizeExhaustedLegacyTicketRecovery(job *jobs.Job) {
	if d.store == nil {
		return
	}
	run, err := d.store.GetLegacyTicketRecoveryRun(store.LegacyTicketRecoveryVersion)
	if err != nil || run == nil || run.State.Terminal() {
		return
	}
	sources, _ := d.store.ListLegacyTicketRecoverySources(run.Version)
	for _, source := range sources {
		if source.State == "complete" {
			continue
		}
		_ = d.store.SetLegacyTicketRecoverySourceState(run.Version, source.Path, "protected", job.LastError)
	}
	protected, _ := d.store.ProtectedLegacyTicketRecoveryPaths(run.Version)
	result := legacyTicketRecoveryResult{Warnings: []string{"recovery exhausted its transient I/O retries: " + job.LastError}}
	for path := range protected {
		result.Protected = append(result.Protected, path)
	}
	result.Counts.Warnings = len(result.Warnings)
	id, err := d.store.FinishLegacyTicketRecovery(run.Version, store.LegacyTicketRecoveryWarned,
		result.Counts, job.LastError, legacyTicketRecoveryWarning(result, job.LastError), time.Now())
	if err != nil {
		d.logf("legacy ticket recovery: finalize exhausted run: %v", err)
		return
	}
	if id != "" {
		d.publishFact(FactNotificationCreated, id, nil)
	}
	d.startPostLegacyTicketRecovery()
}

func (d *Daemon) startPostLegacyTicketRecovery() {
	d.legacyTicketRecoveryPostOnce.Do(func() {
		d.convertBacklogTicketsToSeeds()
		d.replantStrandedTickets()
		d.pruneLegacyTicketRecoveryBackups()
		go d.runDatabaseBackupLoop()
		go d.runAutomationRetentionSweep()
		go d.runAutomationTicketRetentionSweep()
	})
}

func (d *Daemon) pruneLegacyTicketRecoveryBackups() {
	protected := map[string]struct{}{}
	if d.legacyTicketRecoveryEligible() {
		run, err := d.store.GetLegacyTicketRecoveryRun(store.LegacyTicketRecoveryVersion)
		if err != nil || run == nil || !run.State.Terminal() {
			d.logf("legacy ticket recovery: backup pruning remains fenced")
			return
		}
		protected, err = d.store.ProtectedLegacyTicketRecoveryPaths(run.Version)
		if err != nil {
			d.logf("legacy ticket recovery: read protected backups: %v", err)
			return
		}
	}
	if err := store.PruneBackups(d.backupDir(), backupKeep, protected); err != nil && !os.IsNotExist(err) {
		d.logf("database backup prune: %v", err)
	}
	premigrationDir := ""
	if d.store != nil {
		premigrationDir = store.BackupDirForDatabase(d.store.DatabasePath())
	}
	if premigrationDir != "" {
		if err := store.PrunePremigrationBackups(premigrationDir, store.PremigrationBackupKeep(), protected); err != nil && !os.IsNotExist(err) {
			d.logf("pre-migration backup prune: %v", err)
		}
	}
}
