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

	"github.com/google/uuid"
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
func (d *Daemon) quickCaptureAssetPaths(profileID, quickCapture, id string) (string, string) {
	root := filepath.Join(d.dataRoot, "quick-captures", profileID)
	return filepath.Join(root, ".staging", quickCapture, id+".part"), filepath.Join(root, quickCapture, id)
}
func syncQuickCaptureDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func quickCaptureFile(path, name string) (string, int, error) {
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
func (d *Daemon) quickCapturePut(profileID string, msg *protocol.QuickCaptureAttachmentPutMessage) (*protocol.QuickCaptureAttachmentPutResult, error) {
	if err := requireCanonicalUUID(msg.CaptureID); err != nil {
		return nil, err
	}
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
	d.quickCaptureAssetMu.Lock()
	defer d.quickCaptureAssetMu.Unlock()
	a, err := d.store.QuickCaptureAsset(profileID, msg.CaptureID, msg.AttachmentID)
	if err != nil {
		return nil, err
	}
	if a == nil {
		if msg.Offset != 0 {
			return nil, fmt.Errorf("next_offset=0, asked for %d", msg.Offset)
		}
		if _, err := d.store.QuickCapture(profileID, msg.CaptureID); err == nil {
			return nil, fmt.Errorf("quick capture %s is already saved", msg.CaptureID)
		}
		a = &store.QuickCaptureAsset{ProfileID: profileID, CaptureID: msg.CaptureID, Attachment: protocol.QuickCaptureAttachment{ID: msg.AttachmentID, Name: msg.Name}, State: store.QuickCaptureAssetStaged}
		if err := d.store.SaveQuickCaptureAsset(*a); err != nil {
			return nil, err
		}
	}
	if a.Attachment.Name != msg.Name {
		return nil, fmt.Errorf("attachment %s already has a different name", msg.AttachmentID)
	}
	if a.State == store.QuickCaptureAssetDiscarded {
		return nil, fmt.Errorf("attachment %s was discarded; upload with a new identity", msg.AttachmentID)
	}
	stage, final := d.quickCaptureAssetPaths(profileID, msg.CaptureID, msg.AttachmentID)
	if a.State == store.QuickCaptureAssetStaged {
		if _, err := os.Stat(final); err == nil {
			media, size, err := quickCaptureFile(final, a.Attachment.Name)
			if err != nil {
				return nil, err
			}
			if size != a.Attachment.Bytes {
				return nil, fmt.Errorf("finalized attachment byte receipt mismatch")
			}
			if err := syncQuickCaptureDir(filepath.Dir(final)); err != nil {
				return nil, err
			}
			a.Attachment.MediaType = media
			a.State = store.QuickCaptureAssetReady
			if err := d.store.SaveQuickCaptureAsset(*a); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	path := stage
	immutable := a.State == store.QuickCaptureAssetReady || a.State == store.QuickCaptureAssetCommitted
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
		return &protocol.QuickCaptureAttachmentPutResult{NextOffset: size, Attachment: &a.Attachment}, nil
	}
	a.Attachment.Bytes = size
	if err := d.store.SaveQuickCaptureAsset(*a); err != nil {
		return nil, err
	}
	if !msg.Final {
		return &protocol.QuickCaptureAttachmentPutResult{NextOffset: size}, nil
	}
	if msg.Offset+len(data) != size {
		return nil, fmt.Errorf("final chunk ends at %d, next_offset=%d", msg.Offset+len(data), size)
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := syncQuickCaptureParents(filepath.Dir(stage), d.dataRoot); err != nil {
		return nil, err
	}
	media, size, err := quickCaptureFile(stage, a.Attachment.Name)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0700); err != nil {
		return nil, err
	}
	if err := os.Rename(stage, final); err != nil {
		return nil, err
	}
	if err := syncQuickCaptureDir(filepath.Dir(final)); err != nil {
		return nil, err
	}
	if err := syncQuickCaptureDir(filepath.Dir(stage)); err != nil {
		return nil, err
	}
	if err := syncQuickCaptureDir(filepath.Dir(filepath.Dir(final))); err != nil {
		return nil, err
	}
	crashAt("quick-capture-attachment-installed")
	a.Attachment.MediaType = media
	a.Attachment.Bytes = size
	a.State = store.QuickCaptureAssetReady
	if err := d.store.SaveQuickCaptureAsset(*a); err != nil {
		return nil, err
	}
	return &protocol.QuickCaptureAttachmentPutResult{NextOffset: size, Attachment: &a.Attachment}, nil
}
func (d *Daemon) quickCaptureDownload(profileID string, msg *protocol.QuickCaptureAttachmentGetMessage, chunkBytes int) (*protocol.QuickCaptureAttachmentGetResult, error) {
	if err := requireCanonicalUUID(msg.CaptureID); err != nil {
		return nil, err
	}
	if err := requireCanonicalUUID(msg.AttachmentID); err != nil {
		return nil, err
	}
	d.quickCaptureAssetMu.Lock()
	defer d.quickCaptureAssetMu.Unlock()
	a, err := d.store.QuickCaptureAsset(profileID, msg.CaptureID, msg.AttachmentID)
	if err != nil {
		return nil, err
	}
	if a == nil || (a.State != store.QuickCaptureAssetReady && a.State != store.QuickCaptureAssetCommitted) {
		return nil, fmt.Errorf("attachment %s is not finalized", msg.AttachmentID)
	}
	if msg.Offset < 0 || msg.Offset > a.Attachment.Bytes {
		return nil, fmt.Errorf("attachment bytes=%d, asked for offset %d", a.Attachment.Bytes, msg.Offset)
	}
	_, path := d.quickCaptureAssetPaths(profileID, msg.CaptureID, msg.AttachmentID)
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
	return &protocol.QuickCaptureAttachmentGetResult{DataBase64: base64.StdEncoding.EncodeToString(data), NextOffset: next, Eof: next == a.Attachment.Bytes}, nil
}
func (d *Daemon) quickCaptureDiscard(profileID string, msg *protocol.QuickCaptureAttachmentDiscardMessage) error {
	if err := requireCanonicalUUID(msg.CaptureID); err != nil {
		return err
	}
	if err := requireCanonicalUUID(msg.AttachmentID); err != nil {
		return err
	}
	d.quickCaptureAssetMu.Lock()
	defer d.quickCaptureAssetMu.Unlock()
	a, err := d.store.QuickCaptureAsset(profileID, msg.CaptureID, msg.AttachmentID)
	if err != nil {
		return err
	}
	if a == nil {
		return nil
	}
	if a.State == store.QuickCaptureAssetCommitted {
		return fmt.Errorf("attachment %s belongs to a saved quick capture", msg.AttachmentID)
	}
	a.State = store.QuickCaptureAssetDiscarded
	if err := d.store.SaveQuickCaptureAsset(*a); err != nil {
		return err
	}
	stage, final := d.quickCaptureAssetPaths(profileID, msg.CaptureID, msg.AttachmentID)
	for _, path := range []string{stage, final} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
func (d *Daemon) recoverQuickCaptureAssets() error {
	d.quickCaptureAssetMu.Lock()
	defer d.quickCaptureAssetMu.Unlock()
	profiles, err := d.store.ListProfiles(true)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		if err := d.recoverQuickCaptureProfileAssets(profile.ID); err != nil {
			return err
		}
	}
	return nil
}
func (d *Daemon) recoverQuickCaptureProfileAssets(profileID string) error {
	discarded, err := d.store.QuickCaptureDiscardedAssets(profileID)
	if err != nil {
		return err
	}
	for _, a := range discarded {
		stage, final := d.quickCaptureAssetPaths(profileID, a.CaptureID, a.AttachmentID)
		for _, path := range []string{stage, final} {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	assets, err := d.store.QuickCaptureDraftAssets(profileID)
	if err != nil {
		return err
	}
	for _, draft := range assets {
		if draft.State != protocol.QuickCaptureDraftStateStaged {
			continue
		}
		_, final := d.quickCaptureAssetPaths(profileID, draft.CaptureID, draft.AttachmentID)
		media, size, err := quickCaptureFile(final, draft.Name)
		if os.IsNotExist(err) {
			stage, _ := d.quickCaptureAssetPaths(profileID, draft.CaptureID, draft.AttachmentID)
			if info, err := os.Stat(stage); err == nil {
				if err := d.store.SaveQuickCaptureAsset(store.QuickCaptureAsset{ProfileID: profileID, CaptureID: draft.CaptureID, Attachment: protocol.QuickCaptureAttachment{ID: draft.AttachmentID, Name: draft.Name, Bytes: int(info.Size())}, State: store.QuickCaptureAssetStaged}); err != nil {
					return err
				}
			} else if os.IsNotExist(err) {
				if err := d.store.SaveQuickCaptureAsset(store.QuickCaptureAsset{ProfileID: profileID, CaptureID: draft.CaptureID, Attachment: protocol.QuickCaptureAttachment{ID: draft.AttachmentID, Name: draft.Name}, State: store.QuickCaptureAssetStaged}); err != nil {
					return err
				}
			} else {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if size != draft.NextOffset {
			d.logf("quick capture recovery left attachment %s staged: expected %d bytes, found %d", draft.AttachmentID, draft.NextOffset, size)
			continue
		}
		if err := syncQuickCaptureDir(filepath.Dir(final)); err != nil {
			d.logf("quick capture recovery left attachment %s staged: sync directory: %v", draft.AttachmentID, err)
			continue
		}
		if err := d.store.SaveQuickCaptureAsset(store.QuickCaptureAsset{ProfileID: profileID, CaptureID: draft.CaptureID, Attachment: protocol.QuickCaptureAttachment{ID: draft.AttachmentID, Name: draft.Name, MediaType: media, Bytes: size}, State: store.QuickCaptureAssetReady}); err != nil {
			return err
		}
	}
	return nil
}

func syncQuickCaptureParents(path, root string) error {
	for {
		if err := syncQuickCaptureDir(path); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		path = filepath.Dir(path)
	}
}
