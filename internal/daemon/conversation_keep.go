package daemon

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/toolhome"
)

const conversationKeepKind = "conversation_keep"
const factConversationKeptChanged = "conversation.kept.changed"

// Gate holds outside a sweep are single operations; retry after about one runner tick.
const conversationKeepRetry = time.Second

func conversationKeepGrace() time.Duration {
	if days, err := strconv.Atoi(strings.TrimSpace(os.Getenv("ATTN_CONVERSATION_KEEP_GRACE_DAYS"))); err == nil && days >= 0 {
		return time.Duration(days) * 24 * time.Hour
	}
	return defaultWorktreeSweepIdleDays * 24 * time.Hour
}

func conversationKeepQuiet() time.Duration {
	if quiet, err := time.ParseDuration(os.Getenv("ATTN_CONVERSATION_KEEP_QUIET")); err == nil && quiet >= 0 {
		return quiet
	}
	// Claude prunes after 30 days; one quiet day leaves 29 days to take the copy.
	return 24 * time.Hour
}

func conversationKeepInterval() time.Duration {
	if interval, err := time.ParseDuration(os.Getenv("ATTN_CONVERSATION_KEEP_INTERVAL")); err == nil && interval > 0 {
		return interval
	}
	return time.Hour
}

func (d *Daemon) registerConversationKeepCron(runner *jobs.Runner) {
	handler := func(context.Context, *jobs.Job) (any, error) {
		d.keepConversations(time.Now())
		return nil, nil
	}
	if err := runner.RegisterWith(conversationKeepKind, handler, jobs.HandlerConfig{}); err != nil {
		d.logf("conversation keep: register handler: %v", err)
	}
	if err := runner.RegisterCron(conversationKeepKind+"_tick", conversationKeepInterval(), handler, jobs.HandlerConfig{}); err != nil {
		d.logf("conversation keep: register tick: %v", err)
	}
}

func (d *Daemon) queueConversationKeep() {
	d.queueConversationKeepAfter(0)
}

func (d *Daemon) queueConversationKeepAfter(delay time.Duration) {
	queue := d.jobQueueRef()
	if queue == nil {
		return
	}
	if _, err := queue.Enqueue(conversationKeepKind, jobs.EnqueueOptions{UniqueKey: "all", Delay: delay}); err != nil {
		d.logf("conversation keep: enqueue: %v", err)
	}
}

type conversationKey struct{ agent, resumeID string }

func conversationArchive(agent, resumeID string) string {
	return filepath.Join(config.ConversationsDir(), agent, resumeID+".tar.zst")
}

func (d *Daemon) conversationSeedReferences() (map[conversationKey][]protocol.KeptConversationSeed, error) {
	referenced := make(map[conversationKey][]protocol.KeptConversationSeed)
	after := ""
	for {
		read, _, err := d.runDocQuery(docstore.Query{Namespace: garden.Namespace, Collection: garden.CollectionSeeds, Limit: docstore.MaxLimit, After: after})
		if err != nil {
			return nil, err
		}
		for _, doc := range read.Documents {
			seed, err := garden.Decode(doc.Body)
			if err != nil {
				return nil, err
			}
			if garden.Closed(seed.Status) || seed.LastExecutionID == "" {
				continue
			}
			entry := d.store.SessionLedgerEntry(seed.LastExecutionID)
			if entry == nil {
				continue
			}
			execution, local := d.gardenDispatch(entry.ID)
			if !local || execution.HostKind != garden.HostLocal {
				continue
			}
			resumeID := d.store.GetResumeSessionID(entry.ID)
			if resumeID == "" {
				continue
			}
			key := conversationKey{entry.Agent, resumeID}
			referenced[key] = append(referenced[key], protocol.KeptConversationSeed{ID: doc.ID, Slug: seed.StepSlug, Title: seed.Title})
		}
		if len(read.Documents) < docstore.MaxLimit {
			return referenced, nil
		}
		after = read.Documents[len(read.Documents)-1].ID
	}
}

func (d *Daemon) referencedConversations() (map[conversationKey]bool, error) {
	seeds, err := d.conversationSeedReferences()
	if err != nil {
		return nil, err
	}
	referenced := make(map[conversationKey]bool, len(seeds))
	for key := range seeds {
		referenced[key] = true
	}
	pins, err := d.store.ConversationPins()
	if err != nil {
		return nil, err
	}
	for _, pin := range pins {
		referenced[conversationKey{pin.Agent, pin.ResumeID}] = true
	}
	return referenced, nil
}

func (d *Daemon) keepConversations(now time.Time) {
	if d.store == nil || d.requireHome(garden.Surface) != nil {
		return
	}
	d.conversationKeepMu.Lock()
	defer d.conversationKeepMu.Unlock()
	referenced, err := d.referencedConversations()
	if err != nil {
		d.logf("conversation keep: read open work: %v", err)
		return
	}
	live := make(map[conversationKey]bool)
	for _, session := range d.store.List("") {
		live[conversationKey{session.Agent, d.store.GetResumeSessionID(session.ID)}] = true
	}
	idsByAgent := make(map[string][]string)
	for key := range referenced {
		idsByAgent[key.agent] = append(idsByAgent[key.agent], key.resumeID)
	}
	filesByAgent := make(map[string]map[string][]string)
	for agent, ids := range idsByAgent {
		if files, keeper := agentdriver.ConversationFiles(agentdriver.Get(agent), ids); keeper {
			filesByAgent[agent] = files
		}
	}
	changed := make(map[conversationKey]bool)
	for key := range referenced {
		kept, exists := d.store.KeptConversation(key.agent, key.resumeID)
		if exists && kept.DeletedAt.IsZero() && d.store.RetainKeptConversation(key.agent, key.resumeID) {
			changed[key] = true
		}
		files := filesByAgent[key.agent][key.resumeID]
		if len(files) == 0 {
			continue
		}
		best := store.SessionConversation{NativeID: key.resumeID, TranscriptPath: files[0]}
		bestInfo, err := os.Stat(best.TranscriptPath)
		if err != nil {
			continue
		}
		if live[key] && bestInfo.ModTime().After(now.Add(-conversationKeepQuiet())) {
			continue
		}
		if exists && kept.DeletedAt.IsZero() && bestInfo.Size() <= kept.Bytes {
			if _, err := os.Stat(conversationArchive(key.agent, key.resumeID)); err == nil {
				continue
			}
		}
		if err := d.copyConversation(key.agent, best, files, bestInfo.Size(), now); err != nil {
			d.logf("conversation keep: copy %s/%s: %v", key.agent, key.resumeID, err)
		} else {
			changed[key] = true
		}
	}
	all, err := d.store.KeptConversations()
	if err != nil {
		d.logf("conversation keep: read kept rows: %v", err)
		return
	}
	liveFiles := make(map[string]bool)
	for _, kept := range all {
		if !kept.DeletedAt.IsZero() {
			continue
		}
		key := conversationKey{kept.Agent, kept.ResumeID}
		path := conversationArchive(key.agent, key.resumeID)
		liveFiles[path] = true
	}
	d.retireConversations(all, liveFiles, changed, now)
	err = filepath.WalkDir(config.ConversationsDir(), func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !entry.IsDir() && !liveFiles[path] {
			return os.Remove(path)
		}
		return nil
	})
	if err != nil {
		d.logf("conversation keep: remove leftovers: %v", err)
	}
	d.coalesceSnapshots(func() {
		for key := range changed {
			d.publishFact(factConversationKeptChanged, key.resumeID, nil)
		}
	})
}

func (d *Daemon) retireConversations(all []store.KeptConversation, liveFiles map[string]bool, changed map[conversationKey]bool, now time.Time) {
	if len(all) == 0 {
		return
	}
	// Garden role writes and foreground planting use different guards.
	// Hold both only across reference inspection and archive retirement.
	d.gardenWatchMu.Lock()
	defer d.gardenWatchMu.Unlock()
	err := d.worktreeMaintenance.TryBackgroundRemoval(context.Background(), func(automaticWorktreeCleanupProtection) error {
		referenced, err := d.referencedConversations()
		if err != nil {
			return err
		}
		for _, kept := range all {
			if !kept.DeletedAt.IsZero() {
				continue
			}
			key := conversationKey{kept.Agent, kept.ResumeID}
			if referenced[key] {
				if d.store.RetainKeptConversation(key.agent, key.resumeID) {
					changed[key] = true
				}
				continue
			}
			if kept.ReleasedAt.IsZero() {
				if d.store.ReleaseKeptConversation(key.agent, key.resumeID, now) {
					changed[key] = true
				}
				continue
			}
			if now.Before(kept.ReleasedAt.Add(conversationKeepGrace())) {
				continue
			}
			path := conversationArchive(key.agent, key.resumeID)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				d.logf("conversation keep: delete %s/%s: %v", key.agent, key.resumeID, err)
				continue
			}
			if err := d.store.TombstoneKeptConversation(key.agent, key.resumeID, now, "sweep"); err == nil {
				changed[key] = true
				delete(liveFiles, path)
			} else {
				d.logf("conversation keep: tombstone %s/%s: %v", key.agent, key.resumeID, err)
			}
		}
		return nil
	})
	if errors.Is(err, errAutomaticWorktreeCleanupPreempted) {
		if d.worktreeMaintenance.RunAfterSweep(d.queueConversationKeep) {
			d.logf("conversation keep: retiring copies waits for the worktree sweep")
		} else {
			d.logf("conversation keep: retiring copies yielded to a worktree hold; retrying in %s", conversationKeepRetry)
			d.queueConversationKeepAfter(conversationKeepRetry)
		}
	} else if err != nil {
		d.logf("conversation keep: retire copies: %v", err)
	}
}

func (d *Daemon) copyConversation(agent string, conversation store.SessionConversation, files []string, bytes int64, now time.Time) error {
	home, err := toolhome.Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(config.ConversationsDir(), agent), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Join(config.ConversationsDir(), agent), ".copy-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	encoder, err := zstd.NewWriter(tmp, zstd.WithEncoderLevel(zstd.SpeedDefault), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return err
	}
	archive := tar.NewWriter(encoder)
	writtenNames := make(map[string]bool)
	inputs, err := agentdriver.OpenConversationFiles(home)
	if err != nil {
		return err
	}
	defer inputs.Close()
	for _, path := range files {
		relative, relErr := filepath.Rel(home, path)
		if relErr != nil || !conversationFileAllowed(agent, conversation.NativeID, filepath.ToSlash(relative)) {
			err = fmt.Errorf("invalid %s conversation file: %s", agent, path)
			break
		}
		err = fs.WalkDir(inputs, filepath.ToSlash(relative), func(name string, entry fs.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if !conversationFileAllowed(agent, conversation.NativeID, name) {
				return fmt.Errorf("invalid conversation file: %s", name)
			}
			src, err := inputs.Open(name)
			if err != nil {
				return err
			}
			defer src.Close()
			info, err := src.Stat()
			if err != nil {
				return err
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				return fmt.Errorf("unsupported conversation file: %s", name)
			}
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = name
			if err := archive.WriteHeader(header); err != nil {
				return err
			}
			writtenNames[name] = true
			if info.IsDir() {
				return nil
			}
			_, err = io.Copy(archive, src)
			return err
		})
		if err != nil {
			break
		}
	}
	if err == nil {
		err = appendMissingConversationEntries(archive, conversationArchive(agent, conversation.NativeID), agent, conversation.NativeID, writtenNames)
	}
	err = errors.Join(err, archive.Close(), encoder.Close())
	if err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	info, err := tmp.Stat()
	if err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	dest := conversationArchive(agent, conversation.NativeID)
	previous := tmp.Name() + ".previous"
	if err := os.Link(dest, previous); err != nil && !os.IsNotExist(err) {
		return err
	}
	defer os.Remove(previous)
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return err
	}
	err = d.store.PutKeptConversation(store.KeptConversation{
		ResumeID: conversation.NativeID, Agent: agent, SourcePath: conversation.TranscriptPath,
		Bytes: bytes, StoredBytes: info.Size(), CopiedAt: now,
	})
	if err != nil {
		if _, statErr := os.Stat(previous); statErr == nil {
			err = errors.Join(err, os.Rename(previous, dest))
		} else {
			err = errors.Join(err, os.Remove(dest))
		}
	}
	return err
}

func (d *Daemon) conversationKnown(driver agentdriver.Driver, resumeID string) bool {
	if resumeID == "" {
		return false
	}
	if agentdriver.ResumeAvailable(driver, resumeID) {
		return true
	}
	if d.store == nil {
		return false
	}
	kept, ok := d.store.KeptConversation(driver.Name(), resumeID)
	if !ok || !kept.DeletedAt.IsZero() || kept.Agent != driver.Name() {
		return false
	}
	_, err := os.Stat(conversationArchive(kept.Agent, kept.ResumeID))
	return err == nil
}

func (d *Daemon) conversationReady(driver agentdriver.Driver, resumeID string) bool {
	if resumeID == "" {
		return false
	}
	if d.store == nil {
		return agentdriver.ResumeAvailable(driver, resumeID)
	}
	kept, ok := d.store.KeptConversation(driver.Name(), resumeID)
	if !ok || !kept.DeletedAt.IsZero() || kept.Agent != driver.Name() {
		return agentdriver.ResumeAvailable(driver, resumeID)
	}
	if err := restoreConversationArchive(conversationArchive(kept.Agent, kept.ResumeID), kept.Agent, kept.ResumeID, time.Now()); err != nil {
		d.logf("conversation keep: restore %s/%s: %v", kept.Agent, kept.ResumeID, err)
		return false
	}
	return agentdriver.ResumeAvailable(driver, resumeID)
}

func appendMissingConversationEntries(dst *tar.Writer, path, agent, resumeID string, writtenNames map[string]bool) error {
	src, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer src.Close()
	decoder, err := zstd.NewReader(src, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return err
	}
	defer decoder.Close()
	previous := tar.NewReader(decoder)
	for {
		header, err := previous.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if writtenNames[header.Name] {
			continue
		}
		if !conversationFileAllowed(agent, resumeID, header.Name) {
			return fmt.Errorf("invalid %s archive path: %s", agent, header.Name)
		}
		parts := strings.Split(header.Name, "/")
		if len(parts) == 4 && parts[1] == "projects" && parts[3] == resumeID+".jsonl" {
			continue
		}
		if err := dst.WriteHeader(header); err != nil {
			return err
		}
		if _, err := io.Copy(dst, previous); err != nil {
			return err
		}
		writtenNames[header.Name] = true
	}
}

func restoreConversationArchive(path, agent, resumeID string, now time.Time) error {
	home, err := toolhome.Dir()
	if err != nil {
		return err
	}
	outputs, err := agentdriver.OpenConversationFiles(home)
	if err != nil {
		return err
	}
	defer outputs.Close()
	// An opened archive survives deletion; replacement installs a complete archive by rename.
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	decoder, err := zstd.NewReader(src, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return err
	}
	defer decoder.Close()
	archive := tar.NewReader(decoder)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !conversationFileAllowed(agent, resumeID, header.Name) {
			return fmt.Errorf("invalid %s archive path: %s", agent, header.Name)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			return fmt.Errorf("unsupported archive file: %s", header.Name)
		}
		if err := outputs.Restore(archive, header.Name, header.Typeflag == tar.TypeDir, now); err != nil {
			return err
		}
	}
}

func (d *Daemon) keptConversationForSession(id string) *protocol.KeptConversation {
	entry := d.store.SessionLedgerEntry(id)
	if entry == nil {
		return nil
	}
	kept, ok := d.store.KeptConversation(entry.Agent, d.store.GetResumeSessionID(id))
	if !ok {
		return nil
	}
	pins, err := d.store.ConversationPins()
	if err != nil {
		d.logf("conversation keep: read pins: %v", err)
	}
	return protocolKeptConversation(kept, pins)
}

func protocolKeptConversation(kept store.KeptConversation, pins []store.ConversationPin) *protocol.KeptConversation {
	out := &protocol.KeptConversation{Bytes: int(kept.StoredBytes), CopiedAt: kept.CopiedAt.UTC().Format(time.RFC3339Nano)}
	if !kept.DeletedAt.IsZero() {
		out.DeletedAt = protocol.Ptr(kept.DeletedAt.UTC().Format(time.RFC3339Nano))
		if kept.DeletedBy != "" {
			out.DeletedBy = protocol.Ptr(protocol.KeptConversationDeletedBy(kept.DeletedBy))
		}
	} else {
		for _, pin := range pins {
			if pin.Agent == kept.Agent && pin.ResumeID == kept.ResumeID {
				out.PinnedAt = protocol.Ptr(pin.PinnedAt.UTC().Format(time.RFC3339Nano))
				break
			}
		}
		if out.PinnedAt == nil && !kept.ReleasedAt.IsZero() {
			out.DeleteAfter = protocol.Ptr(kept.ReleasedAt.Add(conversationKeepGrace()).UTC().Format(time.RFC3339Nano))
		}
	}
	return out
}
