package daemon

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
	"golang.org/x/sys/unix"
)

const seedArtifactTransferVersion = 1

const (
	seedTransferStaged    = "staged"
	seedTransferInstalled = "installed"
	seedTransferComplete  = "complete"
)

type seedArtifactTransferReceipt struct {
	Version     int                       `json:"version"`
	ID          string                    `json:"id"`
	SeedID      string                    `json:"seed_id"`
	Operation   string                    `json:"operation"`
	Source      string                    `json:"source"`
	Destination string                    `json:"destination"`
	Filename    string                    `json:"filename"`
	Hash        string                    `json:"hash"`
	Size        int64                     `json:"size"`
	ModTimeNS   int64                     `json:"mod_time_ns"`
	Device      uint64                    `json:"device"`
	Inode       uint64                    `json:"inode"`
	Stage       string                    `json:"stage,omitempty"`
	State       string                    `json:"state"`
	Legacy      *garden.ArtifactReference `json:"legacy,omitempty"`
	UpdatedAt   time.Time                 `json:"updated_at"`
}

type stagedSeedArtifact struct {
	path      string
	hash      string
	size      int64
	modTimeNS int64
	device    uint64
	inode     uint64
}

var seedArtifactImageTypes = map[string]string{
	".bmp":  "image/bmp",
	".gif":  "image/gif",
	".jpeg": "image/jpeg",
	".jpg":  "image/jpeg",
	".png":  "image/png",
	".tif":  "image/tiff",
	".tiff": "image/tiff",
	".webp": "image/webp",
}

var seedArtifactLinkExtensions = map[string]struct{}{
	".bmp": {}, ".gif": {}, ".jpeg": {}, ".jpg": {}, ".markdown": {},
	".md": {}, ".pdf": {}, ".png": {}, ".rst": {}, ".text": {},
	".tif": {}, ".tiff": {}, ".txt": {}, ".webp": {},
}

func seedArtifactSeedFromNotebookPath(raw string) (string, bool) {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(raw)), "/")
	if len(parts) != 3 || parts[0] != "seeds" || strings.HasPrefix(parts[2], ".") {
		return "", false
	}
	if err := garden.ValidateID(parts[1]); err != nil {
		return "", false
	}
	if _, err := seedArtifactFilename(parts[2]); err != nil {
		return "", false
	}
	return parts[1], true
}

func seedArtifactFilename(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("artifact filename is required")
	}
	if raw == "." || raw == ".." || strings.HasPrefix(raw, ".") ||
		strings.ContainsAny(raw, `/\\`) || filepath.Base(raw) != raw {
		return "", fmt.Errorf("%q is not a direct visible artifact filename", raw)
	}
	return raw, nil
}

func seedArtifactTargetFilename(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.ContainsAny(trimmed, "?#") {
		return "", fmt.Errorf("%q is not an encoded direct artifact target", raw)
	}
	decoded, err := url.PathUnescape(trimmed)
	if err != nil {
		return "", fmt.Errorf("decode artifact filename %q: %w", raw, err)
	}
	return seedArtifactFilename(decoded)
}

func seedArtifactRelativeTarget(filename string) string {
	return url.PathEscape(filename)
}

func (d *Daemon) seedArtifactDir(seedID string, create bool) (string, string, error) {
	if _, _, err := d.readSeed(seedID); err != nil {
		return "", "", err
	}
	root, err := d.notebookRoot()
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(root) == "" {
		return "", "", errors.New("notebook is not configured")
	}
	dir := notebook.SeedArtifactsDir(root, seedID)
	if create {
		rootInfo, statErr := os.Lstat(root)
		if statErr != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
			if statErr == nil {
				statErr = errors.New("not a real directory")
			}
			return "", "", fmt.Errorf("notebook root %q: %w", root, statErr)
		}
		for _, candidate := range []string{filepath.Dir(dir), dir} {
			if err := os.Mkdir(candidate, 0o755); err != nil && !os.IsExist(err) {
				return "", "", fmt.Errorf("create seed artifact directory: %w", err)
			}
			info, err := os.Lstat(candidate)
			if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				if err == nil {
					err = errors.New("not a real directory")
				}
				return "", "", fmt.Errorf("seed artifact directory %q: %w", candidate, err)
			}
		}
	}
	for _, candidate := range []string{filepath.Dir(dir), dir} {
		info, statErr := os.Lstat(candidate)
		if os.IsNotExist(statErr) && !create {
			continue
		}
		if statErr != nil {
			return "", "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", "", fmt.Errorf("seed artifact directory %q is not a real directory", candidate)
		}
	}
	d.ensureNotebookWatcher(root)
	return root, dir, nil
}

func (d *Daemon) seedArtifacts(seedID string) ([]protocol.SeedArtifact, error) {
	_, dir, err := d.seedArtifactDir(seedID, false)
	if err != nil {
		if os.IsNotExist(err) {
			return []protocol.SeedArtifact{}, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []protocol.SeedArtifact{}, nil
	}
	if err != nil {
		return nil, err
	}
	artifacts := make([]protocol.SeedArtifact, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if _, err := seedArtifactFilename(name); err != nil || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			continue
		}
		artifacts = append(artifacts, protocol.SeedArtifact{
			Filename:       name,
			RelativeTarget: seedArtifactRelativeTarget(name),
			Size:           int(info.Size()),
			ModifiedAt:     info.ModTime().UTC().Format(time.RFC3339Nano),
		})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Filename < artifacts[j].Filename })
	return artifacts, nil
}

func (d *Daemon) seedArtifactReferences(seedID string) []protocol.SeedArtifactReference {
	notes, err := d.readNotesDomain(seedID)
	if err != nil {
		d.logf("garden: reading the artifact references of %s: %v", seedID, err)
		return []protocol.SeedArtifactReference{}
	}
	current := garden.CurrentArtifacts(notes)
	out := make([]protocol.SeedArtifactReference, 0, len(current))
	for _, artifact := range current {
		out = append(out, *artifactToProtocol(artifact))
	}
	return out
}

func (d *Daemon) handleSeedArtifactTransfer(conn net.Conn, msg *protocol.SeedArtifactTransferMessage) {
	result, err := d.submitSeedArtifactTransfer(msg)
	if err != nil {
		d.sendGardenError(conn, "artifact", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedArtifactTransferResult: result})
}

func (d *Daemon) handleSeedArtifactTransferWS(client *wsClient, msg *protocol.SeedArtifactTransferMessage) {
	requestID := protocol.Deref(msg.RequestID)
	result, err := d.submitSeedArtifactTransfer(msg)
	reply := protocol.SeedArtifactTransferResultMessage{
		Event: protocol.EventSeedArtifactTransferResult, RequestID: requestID,
		Success: err == nil, Result: result,
	}
	if err != nil {
		reply.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, reply)
}

func (d *Daemon) submitSeedArtifactTransfer(msg *protocol.SeedArtifactTransferMessage) (*protocol.SeedArtifactTransferResult, error) {
	if err := d.requireHome(garden.Surface); err != nil {
		return nil, err
	}
	seedID := strings.TrimSpace(msg.SeedID)
	if _, _, err := d.readSeed(seedID); err != nil {
		return nil, err
	}
	operation := strings.TrimSpace(strings.ToLower(msg.Operation))
	if operation != "move" && operation != "copy" && operation != "detach" {
		return nil, fmt.Errorf("artifact operation %q is not move, copy, or detach", msg.Operation)
	}

	var source, destination, filename string
	var root, dir string
	var err error
	if operation == "detach" {
		root, dir, err = d.seedArtifactDir(seedID, false)
		if err != nil {
			return nil, err
		}
		filename, err = seedArtifactFilename(protocol.Deref(msg.Filename))
		if err != nil {
			return nil, err
		}
		source = filepath.Join(dir, filename)
		destination, err = absoluteTransferPath(protocol.Deref(msg.DestinationPath), "detach destination")
		if err != nil {
			return nil, err
		}
		if withinSeedArtifactDir(dir, destination) {
			return nil, fmt.Errorf("detach destination %q is still inside this seed's artifact directory", destination)
		}
		if msg.SourcePath != nil || msg.LegacyReference != nil {
			return nil, errors.New("detach accepts a filename and destination, not a source path or legacy reference")
		}
	} else {
		source, err = absoluteTransferPath(protocol.Deref(msg.SourcePath), "artifact source")
		if err != nil {
			return nil, err
		}
		filename = protocol.Deref(msg.Filename)
		if filename == "" {
			filename = filepath.Base(source)
		}
		filename, err = seedArtifactFilename(filename)
		if err != nil {
			return nil, err
		}
		if msg.DestinationPath != nil {
			return nil, errors.New("move and copy choose the seed-owned destination; --to belongs to detach")
		}
		root, err = d.notebookRoot()
		if err != nil {
			return nil, err
		}
		dir = notebook.SeedArtifactsDir(root, seedID)
		destination = filepath.Join(dir, filename)
		recovering, receiptErr := recoverableSeedArtifactTransferReceiptExists(root, seedID, operation, source, destination)
		if receiptErr != nil {
			return nil, receiptErr
		}
		if !recovering {
			if err := validateSeedArtifactSource(source); err != nil {
				return nil, err
			}
			if operation == "move" {
				tracked, display, trackErr := gitTrackedSource(source)
				if trackErr != nil {
					return nil, trackErr
				}
				if tracked {
					return nil, errors.New(trackedSeedArtifactMoveRefusal(display))
				}
			}
		}
		root, dir, err = d.seedArtifactDir(seedID, !recovering)
		if err != nil {
			return nil, err
		}
		destination = filepath.Join(dir, filename)
	}

	var legacy *garden.ArtifactReference
	if msg.LegacyReference != nil {
		if operation == "detach" {
			return nil, errors.New("a legacy reference can only be brought into the seed with move or copy")
		}
		validated, validateErr := garden.ValidateArtifact(*artifactFromProtocol(msg.LegacyReference))
		if validateErr != nil {
			return nil, validateErr
		}
		legacy = &validated
	}

	d.seedArtifactMu.Lock()
	defer d.seedArtifactMu.Unlock()
	receipt, recovered, err := d.runSeedArtifactTransfer(root, seedID, operation, source, destination, filename, legacy)
	if err != nil {
		return nil, err
	}
	if legacy != nil {
		if err := d.detachLegacyArtifactReference(seedID, protocol.Deref(msg.SourceSessionID), *legacy); err != nil {
			return nil, fmt.Errorf("artifact transferred but linked file is still associated: %w; run the same command again", err)
		}
	}

	artifact := protocol.SeedArtifact{
		Filename: filename, RelativeTarget: seedArtifactRelativeTarget(filename), Size: int(receipt.Size),
		ModifiedAt: time.Unix(0, receipt.ModTimeNS).UTC().Format(time.RFC3339Nano),
	}
	result := &protocol.SeedArtifactTransferResult{
		OperationID: receipt.ID, SeedID: seedID, Operation: operation, SourcePath: source,
		DestinationPath: destination, RelativeTarget: seedArtifactRelativeTarget(filename), Recovered: recovered,
	}
	if operation != "detach" {
		result.Artifact = &artifact
	}
	d.publishFact(FactGardenArtifactChanged, seedID, nil)
	changed := filepath.ToSlash(filepath.Join("seeds", seedID, filename))
	d.broadcastFsChanged(root, originAgent, changed)
	return result, nil
}

func trackedSeedArtifactMoveRefusal(display string) string {
	return display + " is tracked by Git. Make the file untracked in Git first, then run this command again. Use --copy if it should remain tracked."
}

func withinSeedArtifactDir(dir, candidate string) bool {
	rel, err := filepath.Rel(dir, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func absoluteTransferPath(raw, label string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("%s %q must be absolute so the owning daemon resolves the intended file", label, raw)
	}
	return filepath.Clean(raw), nil
}

func transferReceiptID(seedID, operation, source, destination string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{seedID, operation, source, destination}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func recoverableSeedArtifactTransferReceiptExists(root, seedID, operation, source, destination string) (bool, error) {
	id := transferReceiptID(seedID, operation, source, destination)
	receipt, found, err := readSeedTransferReceipt(root, id)
	if err != nil || !found {
		return found, err
	}
	if receipt.SeedID != seedID || receipt.Operation != operation || receipt.Source != source || receipt.Destination != destination {
		return false, fmt.Errorf("transfer receipt %s does not match this operation", id)
	}
	if receipt.State == seedTransferComplete && completedTransferDestinationMissing(receipt) {
		return false, nil
	}
	return true, nil
}

func (d *Daemon) runSeedArtifactTransfer(root, seedID, operation, source, destination, filename string, legacy *garden.ArtifactReference) (*seedArtifactTransferReceipt, bool, error) {
	id := transferReceiptID(seedID, operation, source, destination)
	receipt, found, err := readSeedTransferReceipt(root, id)
	if err != nil {
		return nil, false, err
	}
	if found && (receipt.SeedID != seedID || receipt.Operation != operation || receipt.Source != source || receipt.Destination != destination) {
		return nil, true, fmt.Errorf("transfer receipt %s does not match this operation", id)
	}
	if found && receipt.State == seedTransferComplete && completedTransferDestinationMissing(receipt) {
		found = false
	}
	if !found {
		if _, err := os.Lstat(destination); err == nil {
			return nil, false, fmt.Errorf("destination %q already exists; choose another path", destination)
		} else if !os.IsNotExist(err) {
			return nil, false, err
		}
		staged, err := stageSeedArtifact(source, filepath.Dir(destination))
		if err != nil {
			return nil, false, err
		}
		receipt = &seedArtifactTransferReceipt{
			Version: seedArtifactTransferVersion, ID: id, SeedID: seedID,
			Operation: operation, Source: source, Destination: destination,
			Filename: filename, Hash: staged.hash, Size: staged.size,
			ModTimeNS: staged.modTimeNS, Device: staged.device, Inode: staged.inode,
			Stage: staged.path, State: seedTransferStaged, Legacy: legacy,
		}
		if err := writeSeedTransferReceipt(root, receipt); err != nil {
			_ = os.Remove(staged.path)
			return nil, false, err
		}
	}

	if receipt.State == seedTransferComplete {
		if err := verifyCompletedTransferSource(receipt); err != nil {
			return nil, true, err
		}
		return receipt, true, nil
	}
	if receipt.State == seedTransferStaged {
		if err := installSeedArtifactStage(receipt); err != nil {
			return nil, found, err
		}
		receipt.State = seedTransferInstalled
		receipt.Stage = ""
		if err := writeSeedTransferReceipt(root, receipt); err != nil {
			return nil, found, err
		}
	}
	if receipt.State != seedTransferInstalled {
		return nil, found, fmt.Errorf("transfer receipt %s has unknown state %q", id, receipt.State)
	}
	if operation != "copy" {
		if err := removeUnchangedTransferSource(receipt); err != nil {
			return nil, found, err
		}
	}
	receipt.State = seedTransferComplete
	if err := writeSeedTransferReceipt(root, receipt); err != nil {
		return nil, found, err
	}
	return receipt, found, nil
}

func stageSeedArtifact(source, destinationDir string) (stagedSeedArtifact, error) {
	before, err := os.Lstat(source)
	if err != nil {
		return stagedSeedArtifact{}, fmt.Errorf("inspect source %q: %w", source, err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return stagedSeedArtifact{}, fmt.Errorf("%q is not a regular file", source)
	}
	if info, err := os.Stat(destinationDir); err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("not a directory")
		}
		return stagedSeedArtifact{}, fmt.Errorf("destination directory %q: %w", destinationDir, err)
	}
	in, openedInfo, err := openRegularNoFollow(source)
	if err != nil {
		return stagedSeedArtifact{}, err
	}
	defer in.Close()
	if !os.SameFile(before, openedInfo) {
		return stagedSeedArtifact{}, fmt.Errorf("source %q changed before it could be read", source)
	}
	stage, err := os.CreateTemp(destinationDir, ".seed-transfer-*")
	if err != nil {
		return stagedSeedArtifact{}, err
	}
	stagePath := stage.Name()
	removeStage := true
	defer func() {
		if removeStage {
			_ = os.Remove(stagePath)
		}
	}()
	hasher := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(stage, hasher), in)
	syncErr := stage.Sync()
	chmodErr := stage.Chmod(before.Mode().Perm())
	closeErr := stage.Close()
	if err := errors.Join(copyErr, syncErr, chmodErr, closeErr); err != nil {
		return stagedSeedArtifact{}, err
	}
	after, err := os.Lstat(source)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || n != before.Size() {
		return stagedSeedArtifact{}, fmt.Errorf("source %q changed while it was being copied; nothing was installed", source)
	}
	device, inode := fileIdentity(before)
	removeStage = false
	return stagedSeedArtifact{
		path: stagePath, hash: hex.EncodeToString(hasher.Sum(nil)), size: n,
		modTimeNS: before.ModTime().UnixNano(), device: device, inode: inode,
	}, nil
}

func validateSeedArtifactSource(source string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect source %q: %w", source, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", source)
	}
	return nil
}

func fileIdentity(info os.FileInfo) (uint64, uint64) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stat.Dev), uint64(stat.Ino)
	}
	return 0, 0
}

func installSeedArtifactStage(receipt *seedArtifactTransferReceipt) error {
	if receipt.Stage == "" {
		return errors.New("staged transfer has no staging file")
	}
	if err := os.Link(receipt.Stage, receipt.Destination); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("install artifact without replacement: %w", err)
		}
		hash, hashErr := hashRegularFile(receipt.Destination)
		if hashErr != nil || hash != receipt.Hash {
			return fmt.Errorf("destination %q already exists; choose another path", receipt.Destination)
		}
	}
	if err := syncDirectory(filepath.Dir(receipt.Destination)); err != nil {
		return fmt.Errorf("sync artifact directory: %w", err)
	}
	if err := os.Remove(receipt.Stage); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove artifact staging file: %w", err)
	}
	return nil
}

func removeUnchangedTransferSource(receipt *seedArtifactTransferReceipt) error {
	info, err := os.Lstat(receipt.Source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("destination is installed at %q, but source %q is no longer the same regular file; it was not removed", receipt.Destination, receipt.Source)
	}
	device, inode := fileIdentity(info)
	hash, hashErr := hashRegularFile(receipt.Source)
	if hashErr != nil || info.Size() != receipt.Size || info.ModTime().UnixNano() != receipt.ModTimeNS ||
		device != receipt.Device || inode != receipt.Inode || hash != receipt.Hash {
		return fmt.Errorf("destination is installed at %q, but source %q changed; the newer source was not removed", receipt.Destination, receipt.Source)
	}
	if err := os.Remove(receipt.Source); err != nil {
		return fmt.Errorf("destination is installed at %q, but source %q could not be removed: %w", receipt.Destination, receipt.Source, err)
	}
	if err := syncDirectory(filepath.Dir(receipt.Source)); err != nil {
		return fmt.Errorf("source removed, but its directory could not be synced: %w", err)
	}
	return nil
}

func verifyCompletedTransferSource(receipt *seedArtifactTransferReceipt) error {
	destinationHash, err := hashRegularFile(receipt.Destination)
	if err != nil || destinationHash != receipt.Hash {
		return fmt.Errorf("completed transfer destination %q is missing or changed", receipt.Destination)
	}
	if receipt.Operation != "copy" {
		if _, err := os.Lstat(receipt.Source); os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("transfer already completed at %q, but a file now exists again at source %q", receipt.Destination, receipt.Source)
	}
	if _, err := os.Lstat(receipt.Source); os.IsNotExist(err) {
		return nil
	}
	hash, err := hashRegularFile(receipt.Source)
	if err != nil || hash != receipt.Hash {
		return fmt.Errorf("artifact already exists at %q from an earlier copy; choose another filename for the changed source", receipt.Destination)
	}
	return nil
}

func completedTransferDestinationMissing(receipt *seedArtifactTransferReceipt) bool {
	_, err := os.Lstat(receipt.Destination)
	return os.IsNotExist(err)
}

func hashRegularFile(path string) (string, error) {
	f, _, err := openRegularNoFollow(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func openRegularNoFollow(path string) (*os.File, os.FileInfo, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, fmt.Errorf("%q is not a regular file", path)
	}
	return f, info, nil
}

func seedTransferReceiptPath(root, id string) string {
	return filepath.Join(notebook.SeedArtifactTransfersDir(root), id+".json")
}

func readSeedTransferReceipt(root, id string) (*seedArtifactTransferReceipt, bool, error) {
	path := seedTransferReceiptPath(root, id)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var receipt seedArtifactTransferReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return nil, false, fmt.Errorf("read transfer receipt %s: %w", id, err)
	}
	if receipt.Version != seedArtifactTransferVersion || receipt.ID != id {
		return nil, false, fmt.Errorf("transfer receipt %s has an unsupported identity or version", id)
	}
	return &receipt, true, nil
}

func writeSeedTransferReceipt(root string, receipt *seedArtifactTransferReceipt) error {
	dir := notebook.SeedArtifactTransfersDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	receipt.UpdatedAt = time.Now().UTC()
	tmp, err := os.CreateTemp(dir, ".receipt-*")
	if err != nil {
		return err
	}
	path := tmp.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(path)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	encodeErr := json.NewEncoder(tmp).Encode(receipt)
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if err := errors.Join(encodeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Rename(path, seedTransferReceiptPath(root, receipt.ID)); err != nil {
		return err
	}
	if err := syncDirectory(dir); err != nil {
		return err
	}
	remove = false
	return nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func gitTrackedSource(source string) (bool, string, error) {
	resolvedSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		return false, source, fmt.Errorf("resolve source for Git tracking check: %w", err)
	}
	dir := filepath.Dir(resolvedSource)
	rootRaw, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return false, source, nil
		}
		return false, source, fmt.Errorf("check source repository: %w", err)
	}
	root := strings.TrimSpace(string(rootRaw))
	rel, err := filepath.Rel(root, resolvedSource)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, source, nil
	}
	display := filepath.ToSlash(rel)
	cmd := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", ":(literal)"+display)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return true, display, nil
	} else if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return false, display, nil
	} else {
		return false, display, fmt.Errorf("check whether %s is tracked by Git: %w: %s", display, err, strings.TrimSpace(string(output)))
	}
}

func (d *Daemon) detachLegacyArtifactReference(seedID, authorSession string, legacy garden.ArtifactReference) error {
	for _, current := range d.seedArtifactReferences(seedID) {
		candidate := artifactFromProtocol(&current)
		if candidate != nil && candidate.Identity() == legacy.Identity() {
			_, err := d.appendSeedNote(seedID, "", authorSession, "", garden.NoteKindDetach, &legacy)
			return err
		}
	}
	return nil
}

func (d *Daemon) handleSeedArtifactTarget(client *wsClient, msg *protocol.SeedArtifactTargetMessage) {
	result := protocol.SeedArtifactTargetResultMessage{
		Event: protocol.EventSeedArtifactTargetResult, RequestID: msg.RequestID,
	}
	fail := func(err error) {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
	}
	if err := d.requireHome(garden.Surface); err != nil {
		fail(err)
		return
	}
	filename, err := seedArtifactTargetFilename(msg.RelativeTarget)
	if err != nil {
		fail(err)
		return
	}
	_, dir, err := d.seedArtifactDir(msg.SeedID, false)
	if err != nil {
		fail(err)
		return
	}
	path := filepath.Join(dir, filename)
	info, err := os.Lstat(path)
	if err != nil {
		fail(err)
		return
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		fail(fmt.Errorf("%q is not a regular managed artifact", filename))
		return
	}
	target := &protocol.SeedArtifactTargetResult{RelativeTarget: msg.RelativeTarget}
	switch strings.TrimSpace(msg.Purpose) {
	case "artifact":
		target.Path = protocol.Ptr(path)
	case "link":
		if _, ok := seedArtifactLinkExtensions[strings.ToLower(filepath.Ext(filename))]; !ok {
			fail(fmt.Errorf("%q is not a safe Markdown document or image target", filename))
			return
		}
		target.Path = protocol.Ptr(path)
	case "image":
		mimeType, ok := seedArtifactImageTypes[strings.ToLower(filepath.Ext(filename))]
		if !ok {
			fail(fmt.Errorf("%q is not a supported image artifact", filename))
			return
		}
		file, openedInfo, openErr := openRegularNoFollow(path)
		if openErr != nil {
			fail(openErr)
			return
		}
		if !os.SameFile(info, openedInfo) {
			_ = file.Close()
			fail(fmt.Errorf("%q changed before it could be read", filename))
			return
		}
		content, readErr := io.ReadAll(io.LimitReader(file, maxAssetBytes+1))
		closeErr := file.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			fail(err)
			return
		}
		if len(content) > maxAssetBytes {
			fail(fmt.Errorf("image artifact exceeds the %d byte read cap", maxAssetBytes))
			return
		}
		target.MimeType = protocol.Ptr(mimeType)
		target.DataBase64 = protocol.Ptr(base64.StdEncoding.EncodeToString(content))
	default:
		fail(fmt.Errorf("artifact target purpose %q is not image, link, or artifact", msg.Purpose))
		return
	}
	result.Success = true
	result.Result = target
	d.sendToClient(client, result)
}
