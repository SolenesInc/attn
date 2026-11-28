package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/views"
)

// builtInViewDocument is where a tile that names attn's view name in the
// profile reads it: the profile's folder for that view at the home, which
// holds what attn ships until an edit keeps an override there.
func (d *Daemon) builtInViewDocument(profileID, name string) (tileDocument, error) {
	view, overridden, err := d.store.KeptView(profileID, name)
	if err != nil {
		return tileDocument{}, err
	}
	folder, err := keptViewFolder(profileID, name)
	if err != nil {
		return tileDocument{}, err
	}
	file := filepath.Join(folder, views.ViewFile)
	if !overridden {
		return tileDocument{path: file}, d.shipBuiltInView(profileID, name)
	}
	if view.FolderVersion != view.Version {
		d.placeKeptViewLater(profileID, name)
	}
	return tileDocument{path: file}, nil
}

// shipBuiltInView writes what attn ships as its view name into the profile's
// folder for it, unless the folder already holds it.
func (d *Daemon) shipBuiltInView(profileID, name string) error {
	d.shipping.Lock()
	defer d.shipping.Unlock()
	files, _ := views.Shipped(name)
	folder, err := keptViewFolder(profileID, name)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(filepath.Join(folder, views.ViewFile))
	if err == nil && bytes.Equal(current, files[views.ViewFile]) {
		return nil
	}
	write := protocol.ViewFolderWriteMessage{ProfileID: profileID, View: name, Files: encodedViewFiles(files)}
	_, err = d.writeViewFolder(context.Background(), write)
	return err
}
