package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/store"
)

type legacyRecoveryFragment struct {
	Kind       string                    `json:"kind"`
	TicketID   string                    `json:"ticket_id,omitempty"`
	SourcePath string                    `json:"source_path,omitempty"`
	Detail     string                    `json:"detail,omitempty"`
	Transcript *legacyTranscriptFragment `json:"transcript,omitempty"`
	Files      []legacyNotebookFragment  `json:"files,omitempty"`
	Payload    json.RawMessage           `json:"payload,omitempty"`
}

type legacyTranscriptFragment struct {
	Provider        string `json:"provider"`
	NativeSessionID string `json:"native_session_id"`
	State           string `json:"state,omitempty"`
	Timestamp       string `json:"timestamp,omitempty"`
	Bound           bool   `json:"bound"`
	Production      bool   `json:"production"`
	Explicit        bool   `json:"explicit"`
	Fingerprint     string `json:"fingerprint,omitempty"`
}

type legacyNotebookFragment struct {
	Filename  string `json:"filename"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	ModTimeNS int64  `json:"mod_time_ns"`
	SHA256    string `json:"sha256"`
}

type legacyDelegationProvenance struct {
	Family        string `json:"family"`
	Path          string `json:"path"`
	SchemaVersion int    `json:"schema_version"`
	Size          int64  `json:"size,omitempty"`
	ModTimeNS     int64  `json:"mod_time_ns,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
}

type legacyDelegationSalvage struct {
	Row     store.LegacyDelegationOperation `json:"row"`
	Sources []legacyDelegationProvenance    `json:"sources"`
}

func (d *Daemon) recoverLegacyTicketNotebook(ctx context.Context, job *jobs.Job, run *store.LegacyTicketRecoveryRun, result *legacyTicketRecoveryResult) error {
	if testing.Testing() && strings.TrimSpace(d.store.GetSetting(SettingNotebookRoot)) == "" {
		return nil
	}
	root, err := d.notebookRoot()
	if err != nil {
		result.Warnings = append(result.Warnings, "configured Notebook could not be resolved: "+err.Error())
		return nil
	}
	ticketsDir := filepath.Clean(notebook.TicketArtifactsDir(root, ""))
	info, err := os.Lstat(ticketsDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		result.Warnings = append(result.Warnings, "configured Notebook ticket directory could not be read: "+err.Error())
		return nil
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		result.Warnings = append(result.Warnings, "configured Notebook ticket path is not a direct directory: "+ticketsDir)
		return nil
	}
	entries, err := os.ReadDir(ticketsDir)
	if err != nil {
		result.Warnings = append(result.Warnings, "configured Notebook ticket directory could not be listed: "+err.Error())
		return nil
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		ticketID := entry.Name()
		dirPath := filepath.Join(ticketsDir, ticketID)
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			continue
		}
		if err := store.ValidateTicketID(ticketID); err != nil {
			result.fragments = append(result.fragments, legacyRecoveryFragment{
				Kind: "notebook_invalid_ticket_directory", SourcePath: dirPath, Detail: err.Error(),
			})
			continue
		}
		files, skipped, err := d.readLegacyNotebookFiles(ticketID, dirPath)
		result.fragments = append(result.fragments, skipped...)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Notebook ticket directory %s could not be inspected: %v", ticketID, err))
			result.fragments = append(result.fragments, legacyRecoveryFragment{
				Kind: "notebook_read_error", TicketID: ticketID, SourcePath: dirPath, Detail: err.Error(),
			})
			continue
		}
		if len(files) == 0 {
			continue
		}
		ticket, err := d.store.GetTicket(ticketID)
		if err != nil {
			return err
		}
		if ticket == nil {
			result.fragments = append(result.fragments, legacyRecoveryFragment{
				Kind: "notebook_unbound", TicketID: ticketID, SourcePath: dirPath, Files: files,
			})
			continue
		}
		for _, file := range files {
			fingerprint := legacyNotebookAttachmentFingerprint(ticketID, file)
			item := store.LegacyTicketRecoveryItem{
				Fingerprint: fingerprint, RunVersion: run.Version, SourceKind: "notebook",
				SourceKey: file.Path, TicketID: ticketID, CreatedAt: run.RecoveryAt,
			}
			createdAt := time.Unix(0, file.ModTimeNS).UTC()
			attachment := store.TicketAttachment{
				TicketID: ticketID, Filename: file.Filename, Path: file.Path,
				Note: "Recovered from the legacy Notebook; SHA-256 " + file.SHA256, CreatedAt: createdAt,
			}
			var restored string
			if err := withLegacyRecoveryCommit(job, func() error {
				var restoreErr error
				restored, restoreErr = d.store.RestoreLegacyTicketAttachment(attachment, item)
				return restoreErr
			}); err != nil {
				return err
			}
			switch restored {
			case "recovered":
				result.Counts.NotebookAttachments++
			case "live_won":
				message := fmt.Sprintf("Notebook metadata for %s/%s conflicts with an existing attachment; the existing record won", ticketID, file.Filename)
				result.Warnings = append(result.Warnings, message)
				result.fragments = append(result.fragments, legacyRecoveryFragment{
					Kind: "notebook_attachment_conflict", TicketID: ticketID, SourcePath: file.Path,
					Detail: message, Files: []legacyNotebookFragment{file},
				})
			}
		}
	}
	return nil
}

func (d *Daemon) readLegacyNotebookFiles(ticketID, dir string) ([]legacyNotebookFragment, []legacyRecoveryFragment, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	var out []legacyNotebookFragment
	var skipped []legacyRecoveryFragment
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			detail := "nested directory ignored"
			if entry.Type()&os.ModeSymlink != 0 {
				detail = "symlink ignored"
			}
			skipped = append(skipped, legacyRecoveryFragment{
				Kind: "notebook_unsupported_entry", TicketID: ticketID, SourcePath: path, Detail: detail,
			})
			continue
		}
		before, err := d.readLegacyTicketSnapshotIdentity(path)
		if err != nil {
			return nil, skipped, err
		}
		after, err := d.readLegacyTicketSnapshotIdentity(path)
		if err != nil {
			return nil, skipped, err
		}
		if !legacySnapshotIdentityMatches(before, after) {
			return nil, skipped, fmt.Errorf("%s changed during inspection", path)
		}
		out = append(out, legacyNotebookFragment{
			Filename: entry.Name(), Path: before.Path, Size: before.Size,
			ModTimeNS: before.ModTimeNS, SHA256: before.SHA256,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Filename < out[j].Filename })
	return out, skipped, nil
}

func legacyNotebookAttachmentFingerprint(ticketID string, file legacyNotebookFragment) string {
	encoded, _ := json.Marshal(struct {
		Version  int
		TicketID string
		File     legacyNotebookFragment
	}{Version: 1, TicketID: ticketID, File: file})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (d *Daemon) salvageLegacyRecoveryEvidence(ctx context.Context, run *store.LegacyTicketRecoveryRun, result *legacyTicketRecoveryResult) error {
	if err := d.salvageLegacyDelegations(ctx, run, result); err != nil {
		return err
	}
	return d.writeLegacyRecoveryFragments(run, result)
}

func (d *Daemon) salvageLegacyDelegations(ctx context.Context, run *store.LegacyTicketRecoveryRun, result *legacyTicketRecoveryResult) error {
	tickets, err := d.store.ListTickets(store.TicketListFilter{IncludeArchived: true})
	if err != nil {
		return err
	}
	liveTickets := make(map[string]struct{}, len(tickets))
	for _, ticket := range tickets {
		if ticket != nil {
			liveTickets[ticket.ID] = struct{}{}
		}
	}
	type grouped struct {
		row     store.LegacyDelegationOperation
		sources map[string]legacyDelegationProvenance
	}
	groups := make(map[string]*grouped)
	add := func(rows []store.LegacyDelegationOperation, provenance legacyDelegationProvenance) bool {
		contributed := false
		for _, row := range rows {
			if strings.TrimSpace(row.TicketID) == "" || (row.State != "completed" && row.State != "failed") {
				continue
			}
			if _, exists := liveTickets[row.TicketID]; exists {
				continue
			}
			contributed = true
			encoded, _ := json.Marshal(row)
			sum := sha256.Sum256(encoded)
			fingerprint := hex.EncodeToString(sum[:])
			group := groups[fingerprint]
			if group == nil {
				group = &grouped{row: row, sources: make(map[string]legacyDelegationProvenance)}
				groups[fingerprint] = group
			}
			key := provenance.Family + "\x00" + provenance.Path
			group.sources[key] = provenance
		}
		return contributed
	}
	liveRows, err := d.store.ListLegacyDelegationOperations()
	if err != nil {
		return err
	}
	add(liveRows, legacyDelegationProvenance{
		Family: "live", Path: d.store.DatabasePath(), SchemaVersion: store.LatestSchemaVersion(),
	})

	sources, err := d.store.ListLegacyTicketRecoverySources(run.Version)
	if err != nil {
		return err
	}
	contributingSources := make(map[string]struct{})
	for _, source := range sources {
		if source.Family != "routine" && source.Family != "premigration" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		before, err := d.readLegacyTicketSnapshotIdentity(source.Path)
		if err != nil || !legacySnapshotIdentityMatches(source, before) {
			if source.State != "protected" {
				d.protectLegacySalvageSource(run.Version, source.Path, "delegation salvage could not verify the frozen source", result)
			}
			continue
		}
		read, err := store.ReadLegacyDelegationOperationsSnapshot(source.Path)
		if err != nil {
			if source.State != "protected" {
				d.protectLegacySalvageSource(run.Version, source.Path, "delegation salvage: "+err.Error(), result)
			}
			continue
		}
		after, err := d.readLegacyTicketSnapshotIdentity(source.Path)
		if err != nil || !legacySnapshotIdentityMatches(source, after) {
			if source.State != "protected" {
				d.protectLegacySalvageSource(run.Version, source.Path, "delegation salvage source changed during inspection", result)
			}
			continue
		}
		if add(read.Operations, legacyDelegationProvenance{
			Family: source.Family, Path: source.Path, SchemaVersion: read.SchemaVersion,
			Size: source.Size, ModTimeNS: source.ModTimeNS, SHA256: source.SHA256,
		}) {
			contributingSources[source.Path] = struct{}{}
		}
	}
	if len(groups) == 0 {
		return nil
	}
	operations := make([]legacyDelegationSalvage, 0, len(groups))
	for _, group := range groups {
		operation := legacyDelegationSalvage{Row: group.row}
		for _, source := range group.sources {
			operation.Sources = append(operation.Sources, source)
		}
		sort.Slice(operation.Sources, func(i, j int) bool {
			if operation.Sources[i].Family != operation.Sources[j].Family {
				return operation.Sources[i].Family < operation.Sources[j].Family
			}
			return operation.Sources[i].Path < operation.Sources[j].Path
		})
		operations = append(operations, operation)
	}
	sort.Slice(operations, func(i, j int) bool {
		a, b := operations[i].Row, operations[j].Row
		if a.TicketID != b.TicketID {
			return a.TicketID < b.TicketID
		}
		if a.UpdatedAt != b.UpdatedAt {
			return a.UpdatedAt < b.UpdatedAt
		}
		if a.RequestID != b.RequestID {
			return a.RequestID < b.RequestID
		}
		return a.OperationID < b.OperationID
	})
	payload := struct {
		Version    int                       `json:"version"`
		Operations []legacyDelegationSalvage `json:"operations"`
	}{Version: 1, Operations: operations}
	content, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	path := filepath.Join(d.dataRoot, "legacy-ticket-delegations.json")
	if err := d.writeLegacyRecoveryArtifact(path, content); err != nil {
		message := fmt.Sprintf("%d unresolved delegation operation(s) could not be saved to %s: %v", len(operations), path, err)
		result.Warnings = append(result.Warnings, message)
		for source := range contributingSources {
			d.protectLegacySalvageSource(run.Version, source, message, result)
		}
		return nil
	}
	result.Counts.DelegationsSalvaged = len(operations)
	result.Artifacts = append(result.Artifacts, path)
	result.Warnings = append(result.Warnings, fmt.Sprintf("%d unresolved delegation operation(s) were preserved in %s", len(operations), path))
	return nil
}

func (d *Daemon) writeLegacyRecoveryFragments(run *store.LegacyTicketRecoveryRun, result *legacyTicketRecoveryResult) error {
	fragments := normalizeLegacyRecoveryFragments(result.fragments)
	if len(fragments) == 0 {
		return nil
	}
	payload := struct {
		Version   int                      `json:"version"`
		Fragments []legacyRecoveryFragment `json:"fragments"`
	}{Version: 1, Fragments: fragments}
	content, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	dir, err := ensureLegacyRecoveryDirectories(d.dataRoot, "legacy-ticket-recovery")
	if err != nil {
		message := "recovery fragments could not create an owner-only directory: " + err.Error()
		result.Warnings = append(result.Warnings, message)
		d.protectLegacyFragmentSources(run.Version, fragments, message, result)
		return nil
	}
	path := filepath.Join(dir, "fragments.json")
	if err := d.writeLegacyRecoveryArtifact(path, content); err != nil {
		message := fmt.Sprintf("%d recovery fragment(s) could not be saved to %s: %v", len(fragments), path, err)
		result.Warnings = append(result.Warnings, message)
		d.protectLegacyFragmentSources(run.Version, fragments, message, result)
		return nil
	}
	result.Counts.FragmentsSalvaged = len(fragments)
	result.Artifacts = append(result.Artifacts, path)
	result.Warnings = append(result.Warnings, fmt.Sprintf("%d unresolved recovery fragment(s) were preserved in %s", len(fragments), path))
	return nil
}

func (d *Daemon) protectLegacyFragmentSources(version int, fragments []legacyRecoveryFragment, detail string, result *legacyTicketRecoveryResult) {
	sources, err := d.store.ListLegacyTicketRecoverySources(version)
	if err != nil {
		result.Warnings = append(result.Warnings, "list fragment sources for protection: "+err.Error())
		return
	}
	known := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		known[source.Path] = struct{}{}
	}
	protected := make(map[string]struct{})
	for _, fragment := range fragments {
		if _, ok := known[fragment.SourcePath]; !ok {
			continue
		}
		if _, done := protected[fragment.SourcePath]; done {
			continue
		}
		protected[fragment.SourcePath] = struct{}{}
		d.protectLegacySalvageSource(version, fragment.SourcePath, detail, result)
	}
}

func normalizeLegacyRecoveryFragments(fragments []legacyRecoveryFragment) []legacyRecoveryFragment {
	byJSON := make(map[string]legacyRecoveryFragment)
	for _, fragment := range fragments {
		encoded, err := json.Marshal(fragment)
		if err == nil {
			byJSON[string(encoded)] = fragment
		}
	}
	keys := make([]string, 0, len(byJSON))
	for key := range byJSON {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]legacyRecoveryFragment, 0, len(keys))
	for _, key := range keys {
		out = append(out, byJSON[key])
	}
	return out
}

func (d *Daemon) writeLegacyRecoveryArtifact(path string, content []byte) error {
	if d.legacyRecoveryArtifactWrite != nil {
		return d.legacyRecoveryArtifactWrite(path, content)
	}
	dir := filepath.Dir(path)
	ownerOnly := filepath.Clean(dir) != filepath.Clean(d.dataRoot)
	if err := requireLegacyDirectDirectory(dir, ownerOnly); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
			return errors.New("existing artifact is not an owner-only regular file")
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(existing, content) {
			return errors.New("existing artifact differs; preserved without overwrite")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	temp, err := os.CreateTemp(dir, ".legacy-recovery-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Link(tempPath, path); err != nil {
		if os.IsExist(err) {
			existing, readErr := os.ReadFile(path)
			if readErr == nil && bytes.Equal(existing, content) {
				return nil
			}
		}
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}

func requireLegacyDirectDirectory(path string, ownerOnly bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("recovery directory %s is not a direct directory", path)
	}
	if ownerOnly && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("recovery directory %s is not owner-only", path)
	}
	return nil
}

func (d *Daemon) protectLegacySalvageSource(version int, path, detail string, result *legacyTicketRecoveryResult) {
	if err := d.store.SetLegacyTicketRecoverySourceState(version, path, "protected", detail); err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("protect recovery source %s: %v", path, err))
		return
	}
	result.Warnings = append(result.Warnings, path+": "+detail)
}
