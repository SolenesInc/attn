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
	queue := d.jobQueueRef()
	if queue == nil {
		return
	}
	if _, err := queue.Enqueue(conversationKeepKind, jobs.EnqueueOptions{UniqueKey: "all"}); err != nil {
		d.logf("conversation keep: enqueue: %v", err)
	}
}

type conversationKey struct{ agent, resumeID string }

func conversationArchive(agent, resumeID string) string {
	return filepath.Join(config.ConversationsDir(), agent, resumeID+".tar.zst")
}

func (d *Daemon) referencedConversations() (map[conversationKey][]string, error) {
	referenced := make(map[conversationKey][]string)
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
			referenced[key] = append(referenced[key], entry.ID)
		}
		if len(read.Documents) < docstore.MaxLimit {
			return referenced, nil
		}
		after = read.Documents[len(read.Documents)-1].ID
	}
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
	changed := make(map[conversationKey]bool)
	for key, sessions := range referenced {
		kept, exists := d.store.KeptConversation(key.agent, key.resumeID)
		if exists && kept.DeletedAt.IsZero() && d.store.RetainKeptConversation(key.agent, key.resumeID) {
			changed[key] = true
		}
		driver := agentdriver.Get(key.agent)
		var best store.SessionConversation
		var bestInfo fs.FileInfo
		for _, id := range sessions {
			conversation := d.store.GetSessionConversation(id)
			info, err := os.Stat(conversation.TranscriptPath)
			if err == nil && (bestInfo == nil || info.Size() > bestInfo.Size()) {
				best, bestInfo = conversation, info
			}
		}
		if bestInfo == nil {
			continue
		}
		files, keeper := agentdriver.ConversationFiles(driver, key.resumeID, best.TranscriptPath)
		if !keeper || len(files) == 0 {
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
		if len(referenced[key]) > 0 {
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
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			d.logf("conversation keep: delete %s/%s: %v", key.agent, key.resumeID, err)
			continue
		}
		if d.store.TombstoneKeptConversation(key.agent, key.resumeID, now) {
			changed[key] = true
			delete(liveFiles, path)
		}
	}
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
	for _, path := range files {
		err = filepath.WalkDir(path, func(path string, entry fs.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				return fmt.Errorf("unsupported conversation file: %s", path)
			}
			relative, err := filepath.Rel(home, path)
			if err != nil || !filepath.IsLocal(relative) {
				return fmt.Errorf("conversation file outside tool home: %s", path)
			}
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = filepath.ToSlash(relative)
			if err := archive.WriteHeader(header); err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			src, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(archive, src)
			return errors.Join(copyErr, src.Close())
		})
		if err != nil {
			break
		}
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
	if _, err := os.Stat(kept.SourcePath); !os.IsNotExist(err) {
		return false
	}
	if err := restoreConversationArchive(conversationArchive(kept.Agent, kept.ResumeID), time.Now()); err != nil {
		d.logf("conversation keep: restore %s/%s: %v", kept.Agent, kept.ResumeID, err)
		return false
	}
	return agentdriver.ResumeAvailable(driver, resumeID)
}

func restoreConversationArchive(path string, now time.Time) error {
	home, err := toolhome.Dir()
	if err != nil {
		return err
	}
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
		relative := filepath.FromSlash(header.Name)
		if !filepath.IsLocal(relative) {
			return fmt.Errorf("archive path outside tool home: %s", header.Name)
		}
		dest := filepath.Join(home, relative)
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(dest, 0700); err != nil {
				return err
			}
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return fmt.Errorf("unsupported archive file: %s", header.Name)
		}
		if _, err := os.Lstat(dest); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return err
		}
		if err := restoreConversationFile(archive, dest, now); err != nil {
			return err
		}
	}
}

func restoreConversationFile(src io.Reader, dest string, now time.Time) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".restore-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := io.Copy(tmp, src); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chtimes(tmp.Name(), now, now); err != nil {
		return err
	}
	// Linking installs atomically and refuses an existing harness file, including a concurrent writer.
	if err := os.Link(tmp.Name(), dest); err != nil && !os.IsExist(err) {
		return err
	}
	return nil
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
	out := &protocol.KeptConversation{Bytes: int(kept.StoredBytes), CopiedAt: kept.CopiedAt.UTC().Format(time.RFC3339Nano)}
	if !kept.DeletedAt.IsZero() {
		out.DeletedAt = protocol.Ptr(kept.DeletedAt.UTC().Format(time.RFC3339Nano))
	} else if !kept.ReleasedAt.IsZero() {
		out.DeleteAfter = protocol.Ptr(kept.ReleasedAt.Add(conversationKeepGrace()).UTC().Format(time.RFC3339Nano))
	}
	return out
}
