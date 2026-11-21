package daemon

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/crashpoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func requireCanonicalUUID(id string) error {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return fmt.Errorf("quick capture and attachment IDs must be canonical UUIDs: %q", id)
	}
	return nil
}

func (d *Daemon) attachmentPaths(profileID, id string) (string, string) {
	root := filepath.Join(d.dataRoot, "attachments", profileID)
	return filepath.Join(root, ".staging", id+".part"), filepath.Join(root, id)
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func syncParents(path, root string) error {
	for {
		if err := syncDir(path); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		path = filepath.Dir(path)
	}
}

func attachmentFile(path, name string) (string, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	var head [512]byte
	n, err := f.Read(head[:])
	if err != nil && err != io.EOF {
		return "", 0, err
	}
	media := http.DetectContentType(head[:n])
	if media == "application/octet-stream" {
		if extension := mime.TypeByExtension(filepath.Ext(name)); extension != "" {
			media = extension
		}
	}
	return media, int(stat.Size()), nil
}

func (d *Daemon) checkAttachmentOwner(profileID string, owner protocol.AttachmentOwner) error {
	switch owner.Kind {
	case protocol.AttachmentOwnerKindQuickCapture:
		if err := requireCanonicalUUID(owner.ID); err != nil {
			return err
		}
		if _, err := d.store.QuickCapture(profileID, owner.ID); err == nil {
			return fmt.Errorf("quick capture %s is already saved", owner.ID)
		}
		return nil
	case protocol.AttachmentOwnerKindAsk:
		return fmt.Errorf("asks do not take files yet")
	}
	return fmt.Errorf("unknown attachment owner kind %q", owner.Kind)
}

func (d *Daemon) attachmentPut(profileID string, msg *protocol.AttachmentPutMessage) (*protocol.AttachmentPutResult, error) {
	if err := requireCanonicalUUID(msg.AttachmentID); err != nil {
		return nil, err
	}
	if msg.Offset < 0 {
		return nil, fmt.Errorf("offset must be nonnegative, asked for %d", msg.Offset)
	}
	data, err := base64.StdEncoding.DecodeString(msg.DataBase64)
	if err != nil {
		return nil, fmt.Errorf("invalid file base64: %w", err)
	}
	d.attachmentMu.Lock()
	defer d.attachmentMu.Unlock()
	a, err := d.store.Attachment(msg.AttachmentID)
	if err != nil {
		return nil, err
	}
	if a == nil {
		if msg.Offset != 0 {
			return nil, fmt.Errorf("next_offset=0, asked for %d", msg.Offset)
		}
		if err := d.checkAttachmentOwner(profileID, msg.Owner); err != nil {
			return nil, err
		}
		a = &store.StoredAttachment{ProfileID: profileID, Owner: msg.Owner, Attachment: protocol.Attachment{ID: msg.AttachmentID, Name: msg.Name}, State: store.AttachmentStaged}
		if err := d.store.SaveAttachment(*a); err != nil {
			return nil, err
		}
	}
	if a.ProfileID != profileID || a.Owner != msg.Owner {
		return nil, fmt.Errorf("attachment %s belongs to another owner", msg.AttachmentID)
	}
	if a.Attachment.Name != msg.Name {
		return nil, fmt.Errorf("attachment %s already has a different name", msg.AttachmentID)
	}
	if a.State == store.AttachmentDiscarded {
		return nil, fmt.Errorf("attachment %s was discarded; upload with a new identity", msg.AttachmentID)
	}
	stage, final := d.attachmentPaths(profileID, msg.AttachmentID)
	if a.State == store.AttachmentStaged {
		if _, err := os.Stat(final); err == nil {
			media, size, err := attachmentFile(final, a.Attachment.Name)
			if err != nil {
				return nil, err
			}
			if size != a.Attachment.Bytes {
				return nil, fmt.Errorf("finalized attachment byte receipt mismatch")
			}
			if err := syncDir(filepath.Dir(final)); err != nil {
				return nil, err
			}
			a.Attachment.MediaType = media
			a.State = store.AttachmentReady
			if err := d.store.SaveAttachment(*a); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	path := stage
	immutable := a.State == store.AttachmentReady || a.State == store.AttachmentCommitted
	if immutable {
		path = final
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	flags := os.O_RDWR | os.O_CREATE
	if immutable {
		flags = os.O_RDONLY
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := int(info.Size())
	if msg.Offset > size {
		return nil, fmt.Errorf("next_offset=%d, asked for %d", size, msg.Offset)
	}
	if msg.Offset < size || immutable {
		if len(data) > size-msg.Offset {
			return nil, fmt.Errorf("replayed chunk extends past next_offset=%d", size)
		}
		prior := make([]byte, len(data))
		if _, err := f.ReadAt(prior, int64(msg.Offset)); err != nil {
			return nil, err
		}
		if !bytes.Equal(data, prior) {
			return nil, fmt.Errorf("attachment %s conflicting bytes at offset %d", msg.AttachmentID, msg.Offset)
		}
	} else {
		if _, err := f.WriteAt(data, int64(msg.Offset)); err != nil {
			return nil, err
		}
		size += len(data)
	}
	if immutable {
		return &protocol.AttachmentPutResult{NextOffset: size, Attachment: &a.Attachment}, nil
	}
	a.Attachment.Bytes = size
	if err := d.store.SaveAttachment(*a); err != nil {
		return nil, err
	}
	if !msg.Final {
		return &protocol.AttachmentPutResult{NextOffset: size}, nil
	}
	if msg.Offset+len(data) != size {
		return nil, fmt.Errorf("final chunk ends at %d, next_offset=%d", msg.Offset+len(data), size)
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := syncParents(filepath.Dir(stage), d.dataRoot); err != nil {
		return nil, err
	}
	media, size, err := attachmentFile(stage, a.Attachment.Name)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(stage, final); err != nil {
		return nil, err
	}
	if err := syncDir(filepath.Dir(final)); err != nil {
		return nil, err
	}
	if err := syncDir(filepath.Dir(stage)); err != nil {
		return nil, err
	}
	crashpoint.At("quick-capture-attachment-installed")
	a.Attachment.MediaType = media
	a.Attachment.Bytes = size
	a.State = store.AttachmentReady
	if err := d.store.SaveAttachment(*a); err != nil {
		return nil, err
	}
	return &protocol.AttachmentPutResult{NextOffset: size, Attachment: &a.Attachment}, nil
}

func (d *Daemon) attachmentDownload(profileID string, msg *protocol.AttachmentGetMessage, chunkBytes int) (*protocol.AttachmentGetResult, error) {
	if err := requireCanonicalUUID(msg.AttachmentID); err != nil {
		return nil, err
	}
	d.attachmentMu.Lock()
	defer d.attachmentMu.Unlock()
	a, err := d.store.Attachment(msg.AttachmentID)
	if err != nil {
		return nil, err
	}
	if a == nil || a.ProfileID != profileID || (a.State != store.AttachmentReady && a.State != store.AttachmentCommitted) {
		return nil, fmt.Errorf("attachment %s is not finalized", msg.AttachmentID)
	}
	if msg.Offset < 0 || msg.Offset > a.Attachment.Bytes {
		return nil, fmt.Errorf("attachment bytes=%d, asked for offset %d", a.Attachment.Bytes, msg.Offset)
	}
	_, path := d.attachmentPaths(profileID, msg.AttachmentID)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	size := min(chunkBytes, a.Attachment.Bytes-msg.Offset)
	data := make([]byte, int(size))
	if _, err := f.ReadAt(data, int64(msg.Offset)); err != nil {
		return nil, err
	}
	next := msg.Offset + size
	return &protocol.AttachmentGetResult{DataBase64: base64.StdEncoding.EncodeToString(data), NextOffset: next, Eof: next == a.Attachment.Bytes}, nil
}

func (d *Daemon) attachmentDiscard(profileID string, msg *protocol.AttachmentDiscardMessage) error {
	if err := requireCanonicalUUID(msg.AttachmentID); err != nil {
		return err
	}
	d.attachmentMu.Lock()
	defer d.attachmentMu.Unlock()
	a, err := d.store.Attachment(msg.AttachmentID)
	if err != nil {
		return err
	}
	if a == nil || a.ProfileID != profileID {
		return nil
	}
	if a.State == store.AttachmentCommitted {
		return fmt.Errorf("attachment %s belongs to a saved quick capture", msg.AttachmentID)
	}
	a.State = store.AttachmentDiscarded
	if err := d.store.SaveAttachment(*a); err != nil {
		return err
	}
	return d.removeAttachmentFiles(*a)
}

func (d *Daemon) removeAttachmentFiles(a store.StoredAttachment) error {
	stage, final := d.attachmentPaths(a.ProfileID, a.Attachment.ID)
	for _, path := range []string{stage, final} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (d *Daemon) recoverAttachments() error {
	d.attachmentMu.Lock()
	defer d.attachmentMu.Unlock()
	if err := d.moveQuickCaptureFiles(); err != nil {
		return err
	}
	discarded, err := d.store.AttachmentsIn(store.AttachmentDiscarded)
	if err != nil {
		return err
	}
	for _, a := range discarded {
		if err := d.removeAttachmentFiles(a); err != nil {
			return err
		}
	}
	staged, err := d.store.AttachmentsIn(store.AttachmentStaged)
	if err != nil {
		return err
	}
	for _, a := range staged {
		if err := d.recoverStagedAttachment(a); err != nil {
			return err
		}
	}
	return nil
}

func (d *Daemon) recoverStagedAttachment(a store.StoredAttachment) error {
	stage, final := d.attachmentPaths(a.ProfileID, a.Attachment.ID)
	media, size, err := attachmentFile(final, a.Attachment.Name)
	if os.IsNotExist(err) {
		a.Attachment.Bytes = 0
		if info, err := os.Stat(stage); err == nil {
			a.Attachment.Bytes = int(info.Size())
		} else if !os.IsNotExist(err) {
			return err
		}
		return d.store.SaveAttachment(a)
	}
	if err != nil {
		return err
	}
	if size != a.Attachment.Bytes {
		d.logf("attachment recovery left %s staged: expected %d bytes, found %d", a.Attachment.ID, a.Attachment.Bytes, size)
		return nil
	}
	if err := syncDir(filepath.Dir(final)); err != nil {
		d.logf("attachment recovery left %s staged: sync directory: %v", a.Attachment.ID, err)
		return nil
	}
	a.Attachment.MediaType = media
	a.State = store.AttachmentReady
	return d.store.SaveAttachment(a)
}

func (d *Daemon) moveQuickCaptureFiles() error {
	old := filepath.Join(d.dataRoot, "quick-captures")
	profiles, err := os.ReadDir(old)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		if !profile.IsDir() {
			continue
		}
		from := filepath.Join(old, profile.Name())
		staging := filepath.Join(from, ".staging")
		committed := func(id string) string { _, final := d.attachmentPaths(profile.Name(), id); return final }
		staged := func(part string) string {
			stage, _ := d.attachmentPaths(profile.Name(), strings.TrimSuffix(part, ".part"))
			return stage
		}
		if err := moveCaptureFolders(staging, staged); err != nil {
			return err
		}
		if err := moveCaptureFolders(from, committed); err != nil {
			return err
		}
		for _, dir := range []string{filepath.Join(d.dataRoot, "attachments", profile.Name(), ".staging"), filepath.Join(d.dataRoot, "attachments", profile.Name())} {
			if err := syncDir(dir); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		d.removeEmptyDir(staging)
		d.removeEmptyDir(from)
	}
	d.removeEmptyDir(old)
	return nil
}

func moveCaptureFolders(root string, destination func(name string) string) error {
	captures, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, capture := range captures {
		if !capture.IsDir() || capture.Name() == ".staging" {
			continue
		}
		folder := filepath.Join(root, capture.Name())
		files, err := os.ReadDir(folder)
		if err != nil {
			return err
		}
		for _, file := range files {
			to := destination(file.Name())
			if err := os.MkdirAll(filepath.Dir(to), 0700); err != nil {
				return err
			}
			if err := os.Rename(filepath.Join(folder, file.Name()), to); err != nil {
				return err
			}
		}
		if err := os.Remove(folder); err != nil {
			return err
		}
	}
	return nil
}

func (d *Daemon) removeEmptyDir(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		d.logf("attachment move left %s: %v", path, err)
	}
}
