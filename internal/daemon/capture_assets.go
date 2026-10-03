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

func captureID(id string) error {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return fmt.Errorf("capture and attachment IDs must be canonical UUIDs: %q", id)
	}
	return nil
}
func (d *Daemon) captureAssetPaths(capture, id string) (string, string) {
	root := filepath.Join(d.dataRoot, "captures")
	return filepath.Join(root, ".staging", capture, id+".part"), filepath.Join(root, capture, id)
}
func syncCaptureDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func captureFile(path, name string) (string, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	var head [512]byte // http.DetectContentType inspects at most 512 bytes.
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
func (d *Daemon) capturePut(msg *protocol.CaptureAttachmentPutMessage) (*protocol.CaptureAttachmentPutResult, error) {
	if err := captureID(msg.CaptureID); err != nil {
		return nil, err
	}
	if err := captureID(msg.AttachmentID); err != nil {
		return nil, err
	}
	if msg.Offset < 0 {
		return nil, fmt.Errorf("offset must be nonnegative, asked for %d", msg.Offset)
	}
	data, err := base64.StdEncoding.DecodeString(msg.DataBase64)
	if err != nil {
		return nil, fmt.Errorf("invalid file base64: %w", err)
	}
	d.captureAssetMu.Lock()
	defer d.captureAssetMu.Unlock()
	a, err := d.store.CaptureAsset(msg.CaptureID, msg.AttachmentID)
	if err != nil {
		return nil, err
	}
	if a == nil {
		if msg.Offset != 0 {
			return nil, fmt.Errorf("next_offset=0, asked for %d", msg.Offset)
		}
		if _, err := d.store.Capture(msg.CaptureID); err == nil {
			return nil, fmt.Errorf("capture %s is already saved", msg.CaptureID)
		}
		a = &store.CaptureAsset{CaptureID: msg.CaptureID, Attachment: protocol.CaptureAttachment{ID: msg.AttachmentID, Name: msg.Name}, State: "staged"}
		if err := d.store.SaveCaptureAsset(*a); err != nil {
			return nil, err
		}
	}
	if a.Attachment.Name != msg.Name {
		return nil, fmt.Errorf("attachment %s already has a different name", msg.AttachmentID)
	}
	if a.State == "discarded" {
		return nil, fmt.Errorf("attachment %s was discarded; upload with a new identity", msg.AttachmentID)
	}
	stage, final := d.captureAssetPaths(msg.CaptureID, msg.AttachmentID)
	if a.State == "staged" {
		if _, err := os.Stat(final); err == nil {
			media, size, err := captureFile(final, a.Attachment.Name)
			if err != nil {
				return nil, err
			}
			if size != a.Attachment.Bytes {
				return nil, fmt.Errorf("finalized attachment byte receipt mismatch")
			}
			if err := syncCaptureDir(filepath.Dir(final)); err != nil {
				return nil, err
			}
			a.Attachment.MediaType = media
			a.State = "ready"
			if err := d.store.SaveCaptureAsset(*a); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	path := stage
	immutable := a.State == "ready" || a.State == "committed"
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
		return &protocol.CaptureAttachmentPutResult{NextOffset: size, Attachment: &a.Attachment}, nil
	}
	a.Attachment.Bytes = size
	if err := d.store.SaveCaptureAsset(*a); err != nil {
		return nil, err
	}
	if !msg.Final {
		return &protocol.CaptureAttachmentPutResult{NextOffset: size}, nil
	}
	if msg.Offset+len(data) != size {
		return nil, fmt.Errorf("final chunk ends at %d, next_offset=%d", msg.Offset+len(data), size)
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	for _, dir := range []string{filepath.Dir(stage), filepath.Dir(filepath.Dir(stage)), filepath.Dir(filepath.Dir(filepath.Dir(stage))), d.dataRoot} {
		if err := syncCaptureDir(dir); err != nil {
			return nil, err
		}
	}
	media, size, err := captureFile(stage, a.Attachment.Name)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0700); err != nil {
		return nil, err
	}
	if err := os.Rename(stage, final); err != nil {
		return nil, err
	}
	if err := syncCaptureDir(filepath.Dir(final)); err != nil {
		return nil, err
	}
	if err := syncCaptureDir(filepath.Dir(stage)); err != nil {
		return nil, err
	}
	if err := syncCaptureDir(filepath.Dir(filepath.Dir(final))); err != nil {
		return nil, err
	}
	crashAt("capture-attachment-installed")
	a.Attachment.MediaType = media
	a.Attachment.Bytes = size
	a.State = "ready"
	if err := d.store.SaveCaptureAsset(*a); err != nil {
		return nil, err
	}
	return &protocol.CaptureAttachmentPutResult{NextOffset: size, Attachment: &a.Attachment}, nil
}
func (d *Daemon) captureDownload(msg *protocol.CaptureAttachmentGetMessage, chunkBytes int) (*protocol.CaptureAttachmentGetResult, error) {
	if err := captureID(msg.CaptureID); err != nil {
		return nil, err
	}
	if err := captureID(msg.AttachmentID); err != nil {
		return nil, err
	}
	d.captureAssetMu.Lock()
	defer d.captureAssetMu.Unlock()
	a, err := d.store.CaptureAsset(msg.CaptureID, msg.AttachmentID)
	if err != nil {
		return nil, err
	}
	if a == nil || (a.State != "ready" && a.State != "committed") {
		return nil, fmt.Errorf("attachment %s is not finalized", msg.AttachmentID)
	}
	if msg.Offset < 0 || msg.Offset > a.Attachment.Bytes {
		return nil, fmt.Errorf("attachment bytes=%d, asked for offset %d", a.Attachment.Bytes, msg.Offset)
	}
	_, path := d.captureAssetPaths(msg.CaptureID, msg.AttachmentID)
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
	return &protocol.CaptureAttachmentGetResult{DataBase64: base64.StdEncoding.EncodeToString(data), NextOffset: next, Eof: next == a.Attachment.Bytes}, nil
}
func (d *Daemon) captureDiscard(msg *protocol.CaptureAttachmentDiscardMessage) error {
	if err := captureID(msg.CaptureID); err != nil {
		return err
	}
	if err := captureID(msg.AttachmentID); err != nil {
		return err
	}
	d.captureAssetMu.Lock()
	defer d.captureAssetMu.Unlock()
	a, err := d.store.CaptureAsset(msg.CaptureID, msg.AttachmentID)
	if err != nil {
		return err
	}
	if a == nil {
		return nil
	}
	if a.State == "committed" {
		return fmt.Errorf("attachment %s belongs to a saved capture", msg.AttachmentID)
	}
	a.State = "discarded"
	if err := d.store.SaveCaptureAsset(*a); err != nil {
		return err
	}
	stage, final := d.captureAssetPaths(msg.CaptureID, msg.AttachmentID)
	for _, path := range []string{stage, final} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
func (d *Daemon) recoverCaptureAssets() error {
	d.captureAssetMu.Lock()
	defer d.captureAssetMu.Unlock()
	discarded, err := d.store.CaptureDiscardedAssets()
	if err != nil {
		return err
	}
	for _, a := range discarded {
		stage, final := d.captureAssetPaths(a.CaptureID, a.AttachmentID)
		for _, path := range []string{stage, final} {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	assets, err := d.store.CaptureDraftAssets()
	if err != nil {
		return err
	}
	for _, draft := range assets {
		if draft.State != "staged" {
			continue
		}
		_, final := d.captureAssetPaths(draft.CaptureID, draft.AttachmentID)
		media, size, err := captureFile(final, draft.Name)
		if os.IsNotExist(err) {
			stage, _ := d.captureAssetPaths(draft.CaptureID, draft.AttachmentID)
			if info, err := os.Stat(stage); err == nil {
				if err := d.store.SaveCaptureAsset(store.CaptureAsset{CaptureID: draft.CaptureID, Attachment: protocol.CaptureAttachment{ID: draft.AttachmentID, Name: draft.Name, Bytes: int(info.Size())}, State: "staged"}); err != nil {
					return err
				}
			} else if os.IsNotExist(err) {
				if err := d.store.SaveCaptureAsset(store.CaptureAsset{CaptureID: draft.CaptureID, Attachment: protocol.CaptureAttachment{ID: draft.AttachmentID, Name: draft.Name}, State: "staged"}); err != nil {
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
			d.logf("capture recovery left attachment %s staged: expected %d bytes, found %d", draft.AttachmentID, draft.NextOffset, size)
			continue
		}
		if err := syncCaptureDir(filepath.Dir(final)); err != nil {
			d.logf("capture recovery left attachment %s staged: sync directory: %v", draft.AttachmentID, err)
			continue
		}
		if err := d.store.SaveCaptureAsset(store.CaptureAsset{CaptureID: draft.CaptureID, Attachment: protocol.CaptureAttachment{ID: draft.AttachmentID, Name: draft.Name, MediaType: media, Bytes: size}, State: "ready"}); err != nil {
			return err
		}
	}
	return nil
}
