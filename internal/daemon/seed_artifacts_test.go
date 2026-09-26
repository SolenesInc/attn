package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

func newSeedArtifactDaemon(t *testing.T) (*Daemon, string, protocol.Seed) {
	t.Helper()
	d := newGardenDaemon(t)
	root := t.TempDir()
	d.store.SetSetting(SettingNotebookRoot, root)
	t.Cleanup(d.stopNotebookWatcher)
	return d, root, plant(t, d, protocol.SeedPlantMessage{Title: "Durable files"})
}

func writeArtifactSource(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSeedArtifactInstalledRecoveryNeverDeletesANewerSource(t *testing.T) {
	d, root, seed := newSeedArtifactDaemon(t)
	source := writeArtifactSource(t, t.TempDir(), "recover.bin", []byte("old"))
	destination := filepath.Join(notebook.SeedArtifactsDir(root, seed.ID), "recover.bin")
	if _, _, err := d.seedArtifactDir(seed.ID, true); err != nil {
		t.Fatal(err)
	}
	staged, err := stageSeedArtifact(source, filepath.Dir(destination))
	if err != nil {
		t.Fatal(err)
	}
	receipt := receiptFromStage(seed.ID, "move", source, destination, "recover.bin", staged)
	if err := installSeedArtifactStage(receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Stage = ""
	receipt.State = seedTransferInstalled
	if err := writeSeedTransferReceipt(root, receipt); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("newer"), 0o640); err != nil {
		t.Fatal(err)
	}

	_, recovered, err := d.runSeedArtifactTransfer(root, seed.ID, "move", source, destination, "recover.bin", nil)
	if !recovered || err == nil || !strings.Contains(err.Error(), "newer source was not removed") {
		t.Fatalf("recovery = recovered %v, err %v", recovered, err)
	}
	if got, _ := os.ReadFile(source); string(got) != "newer" {
		t.Fatalf("newer source = %q", got)
	}
	if got, _ := os.ReadFile(destination); string(got) != "old" {
		t.Fatalf("installed destination = %q", got)
	}
}

func TestSeedArtifactStagedReceiptRecoversAfterInterruption(t *testing.T) {
	d, root, seed := newSeedArtifactDaemon(t)
	source := writeArtifactSource(t, t.TempDir(), "recover.bin", []byte("payload"))
	destination := filepath.Join(notebook.SeedArtifactsDir(root, seed.ID), "recover.bin")
	if _, _, err := d.seedArtifactDir(seed.ID, true); err != nil {
		t.Fatal(err)
	}
	staged, err := stageSeedArtifact(source, filepath.Dir(destination))
	if err != nil {
		t.Fatal(err)
	}
	receipt := receiptFromStage(seed.ID, "move", source, destination, "recover.bin", staged)
	if err := writeSeedTransferReceipt(root, receipt); err != nil {
		t.Fatal(err)
	}

	got, recovered, err := d.runSeedArtifactTransfer(root, seed.ID, "move", source, destination, "recover.bin", nil)
	if err != nil || !recovered || got.State != seedTransferComplete {
		t.Fatalf("recovered transfer = %+v, %v, %v", got, recovered, err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source after recovered move: %v", err)
	}
	if content, _ := os.ReadFile(destination); string(content) != "payload" {
		t.Fatalf("destination = %q", content)
	}
}

func receiptFromStage(seedID, operation, source, destination, filename string, staged stagedSeedArtifact) *seedArtifactTransferReceipt {
	return &seedArtifactTransferReceipt{
		Version: seedArtifactTransferVersion,
		ID:      transferReceiptID(seedID, operation, source, destination),
		SeedID:  seedID, Operation: operation, Source: source, Destination: destination,
		Filename: filename, Hash: staged.hash, Size: staged.size, ModTimeNS: staged.modTimeNS,
		Device: staged.device, Inode: staged.inode, Stage: staged.path, State: seedTransferStaged,
	}
}
