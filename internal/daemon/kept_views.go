package daemon

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/viewlang"
	"github.com/victorarias/attn/internal/views"
)

// keptViewFolders is how the daemon brings each kept view's folder on its
// machine up to its newest version: one placement at a time per view.
type keptViewFolders struct {
	mu      sync.Mutex
	placing map[keptViewKey]bool
}

type keptViewKey struct {
	profileID string
	name      string
}

var namedFilePattern = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`!\[[^\]]*\]\(([^()\s{}]+)\)`) })

// readViewFolder reads, on this machine, the view file at msg.Path or in the
// folder msg.Path, and the files it names inside its folder.
func (d *Daemon) readViewFolder(_ context.Context, msg protocol.ViewFolderReadMessage) (protocol.ViewFolder, error) {
	viewFile := filepath.Clean(msg.Path)
	if info, err := os.Stat(viewFile); err == nil && info.IsDir() {
		viewFile = filepath.Join(viewFile, views.ViewFile)
	}
	content, err := readMarkdownFile(viewFile)
	if err != nil {
		return protocol.ViewFolder{}, err
	}
	folder := filepath.Dir(viewFile)
	found := protocol.ViewFolder{Files: protocol.RecordString{views.ViewFile: base64.StdEncoding.EncodeToString([]byte(content))}, Missing: []string{}, Bytes: len(content)}
	for _, file := range keptViewFiles(folder, content) {
		name, _ := filepath.Rel(folder, file)
		info, err := os.Stat(file)
		if err != nil || !info.Mode().IsRegular() {
			found.Missing = append(found.Missing, name)
			continue
		}
		found.Bytes += int(info.Size())
		if found.Bytes > views.MaxBytes {
			continue
		}
		data, err := readBounded(file, views.MaxBytes)
		if err != nil {
			return protocol.ViewFolder{}, err
		}
		found.Files[name] = base64.StdEncoding.EncodeToString(data)
	}
	return found, nil
}

func readBounded(path string, limit int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, int64(limit)+1))
}

// keptViewFiles is the files besides the view file that a kept view keeps:
// those its sources read and its commands run, and the pictures and clips it
// names, inside folder.
func keptViewFiles(folder, content string) []string {
	files := viewDataFiles(folder, viewlang.Check(content).Parts)
	for _, match := range namedFilePattern().FindAllStringSubmatch(content, -1) {
		if filepath.IsLocal(match[1]) {
			files = append(files, filepath.Join(folder, match[1]))
		}
	}
	var kept []string
	for _, file := range files {
		if name, _ := filepath.Rel(folder, file); name != views.ViewFile && !slices.Contains(kept, file) {
			kept = append(kept, file)
		}
	}
	return kept
}

func keptViewFolder(profileID, name string) (string, error) {
	if !filepath.IsLocal(profileID) || strings.ContainsRune(profileID, filepath.Separator) || !filepath.IsLocal(name) || strings.ContainsRune(name, filepath.Separator) {
		return "", fmt.Errorf("no kept view %s in profile %s", name, profileID)
	}
	return filepath.Join(config.DataDir(), "views", profileID, name), nil
}

// writeViewFolder makes this machine's folder of a kept view hold exactly
// msg.Files, and the responses the user sent from it; no files removes it.
func (d *Daemon) writeViewFolder(_ context.Context, msg protocol.ViewFolderWriteMessage) (protocol.ViewFolderWritten, error) {
	folder, err := keptViewFolder(msg.ProfileID, msg.View)
	if err != nil {
		return protocol.ViewFolderWritten{}, err
	}
	if len(msg.Files) == 0 {
		return protocol.ViewFolderWritten{Folder: folder}, os.RemoveAll(folder)
	}
	responses := filepath.Base(viewResponsesPath(filepath.Join(folder, views.ViewFile)))
	keep := []string{responses}
	files := stringsOf(msg.Files)
	programs, err := viewPrograms(files[views.ViewFile])
	if err != nil {
		return protocol.ViewFolderWritten{}, err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		if !filepath.IsLocal(name) {
			return protocol.ViewFolderWritten{}, fmt.Errorf("%s is outside the view's folder", name)
		}
		names = append(names, name)
		keep = append(keep, filepath.Clean(name))
	}
	slices.SortFunc(names, func(a, b string) int {
		if (a == views.ViewFile) != (b == views.ViewFile) {
			if a == views.ViewFile {
				return 1
			}
			return -1
		}
		return strings.Compare(a, b)
	})
	for _, name := range names {
		content, err := base64.StdEncoding.DecodeString(files[name])
		if err != nil {
			return protocol.ViewFolderWritten{}, fmt.Errorf("%s: %w", name, err)
		}
		mode := os.FileMode(0o644)
		if slices.Contains(programs, filepath.Clean(name)) {
			mode = 0o755
		}
		if err := writeFileAtomically(filepath.Join(folder, name), content, mode); err != nil {
			return protocol.ViewFolderWritten{}, err
		}
	}
	err = filepath.WalkDir(folder, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		name, _ := filepath.Rel(folder, path)
		if !slices.Contains(keep, name) {
			return os.Remove(path)
		}
		return nil
	})
	return protocol.ViewFolderWritten{Folder: folder}, err
}

// viewPrograms is the files of the view's folder that its commands run as
// their program, which a folder written from the database must make
// executable again.
func viewPrograms(encoded string) ([]string, error) {
	content, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	var programs []string
	for _, command := range viewCommands(viewlang.Check(string(content)).Parts) {
		if words := command.words(); len(words) > 0 && filepath.IsLocal(words[0]) {
			programs = append(programs, filepath.Clean(words[0]))
		}
	}
	return programs, nil
}

// discardKeptViewFiles lets go of the files the user gave the kept view's
// answers and no send gave an agent.
func (d *Daemon) discardKeptViewFiles(view store.KeptView) error {
	answers, err := d.store.ViewAnswers(store.HostPath{Host: d.machineFor(view.MachineID).ID(), Path: view.ViewFile()})
	if err != nil {
		return err
	}
	var given []string
	for _, draft := range answers.Drafts {
		given = append(given, draft.Attachments...)
	}
	d.discardViewFiles(given, nil)
	return nil
}

func writeFileAtomically(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	partial, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(partial.Name())
	if _, err := partial.Write(content); err != nil {
		partial.Close()
		return err
	}
	if err := partial.Close(); err != nil {
		return err
	}
	if err := os.Chmod(partial.Name(), mode); err != nil {
		return err
	}
	return os.Rename(partial.Name(), path)
}

// placeKeptView brings the folder of the kept view on its machine up to its
// newest version, and tells the tiles that show it.
func (d *Daemon) placeKeptView(ctx context.Context, profileID, name string) error {
	key := keptViewKey{profileID: profileID, name: name}
	folders := &d.keptViewFolders
	folders.mu.Lock()
	if folders.placing[key] {
		folders.mu.Unlock()
		return nil
	}
	if folders.placing == nil {
		folders.placing = map[keptViewKey]bool{}
	}
	folders.placing[key] = true
	folders.mu.Unlock()
	defer func() {
		folders.mu.Lock()
		delete(folders.placing, key)
		folders.mu.Unlock()
	}()
	for {
		view, kept, err := d.store.KeptView(profileID, name)
		if err != nil || !kept || view.Folder != "" && view.FolderVersion == view.Version {
			return err
		}
		files, err := d.store.ViewFiles(profileID, name, view.Version)
		if err != nil {
			return err
		}
		write := protocol.ViewFolderWriteMessage{Cmd: protocol.CmdViewFolderWrite, ProfileID: profileID, View: name, Files: encodedViewFiles(files)}
		host := d.machineFor(view.MachineID).Outpost()
		if host != "" {
			write.Host = protocol.Ptr(host)
		}
		written, err := Call[protocol.ViewFolderWritten](ctx, d.router(), write)
		if err != nil {
			return fmt.Errorf("give %s its version %d: %w", name, view.Version, d.tileDocumentError(host, err))
		}
		if err := d.store.SetViewFolder(profileID, name, d.machineFor(view.MachineID).ID(), written.Folder, view.Version); err != nil {
			return err
		}
		d.nudgeDesktopTileContent()
	}
}

// placeKeptViewLater places the kept view's folder off the caller's path,
// for a tile that shows a view whose machine lags its newest version.
func (d *Daemon) placeKeptViewLater(profileID, name string) {
	d.life.Go("placeKeptView", func() {
		if err := d.placeKeptView(d.life.Context(), profileID, name); err != nil {
			d.logf("kept view %s: %v", name, err)
		}
	})
}

func encodedViewFiles(files map[string][]byte) protocol.RecordString {
	encoded := make(protocol.RecordString, len(files))
	for name, content := range files {
		encoded[name] = base64.StdEncoding.EncodeToString(content)
	}
	return encoded
}

func decodedViewFiles(files map[string]string) (views.Files, error) {
	decoded := make(views.Files, len(files))
	for name, content := range files {
		data, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		decoded[name] = data
	}
	return decoded, nil
}

// keptViewDocument is where the tile that names the kept view name in the
// profile reads it: its view file in the folder its machine holds.
func (d *Daemon) keptViewDocument(profileID, name string) (tileDocument, error) {
	view, kept, err := d.store.KeptView(profileID, name)
	if err != nil {
		return tileDocument{}, err
	}
	if !kept {
		return tileDocument{}, fmt.Errorf("the profile keeps no view %s", name)
	}
	if view.FolderVersion != view.Version {
		d.placeKeptViewLater(profileID, name)
	}
	if view.Folder == "" {
		return tileDocument{}, errors.New("the view's machine has not been given it yet")
	}
	return tileDocument{host: d.machineFor(view.MachineID).Outpost(), path: view.ViewFile()}, nil
}

func recordOf(texts map[string]string) protocol.RecordString {
	record := make(protocol.RecordString, len(texts))
	for name, text := range texts {
		record[name] = text
	}
	return record
}

func stringsOf(record protocol.RecordString) map[string]string {
	texts := make(map[string]string, len(record))
	for name, value := range record {
		if text, ok := value.(string); ok {
			texts[name] = text
		}
	}
	return texts
}
