package daemon

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type askImage struct {
	path string
	data []byte
}

func parseAskImages(images []protocol.AskImage) ([]askImage, error) {
	parsed := make([]askImage, 0, len(images))
	total := 0
	for _, image := range images {
		data, err := base64.StdEncoding.DecodeString(image.DataBase64)
		if err != nil {
			return nil, fmt.Errorf("ask image %s: %w", image.Path, err)
		}
		total += len(data)
		parsed = append(parsed, askImage{path: image.Path, data: data})
	}
	if total > ask.MaxImageBytes {
		return nil, fmt.Errorf("an ask's images hold at most max_ask_image_bytes=%d together; these %d images hold %d bytes", ask.MaxImageBytes, len(parsed), total)
	}
	return parsed, nil
}

func askImagesDir(askID string) string {
	return filepath.Join(config.DataDir(), "asks", askID)
}

func askImageFile(askID, imagePath string) string {
	sum := sha256.Sum256([]byte(imagePath))
	return filepath.Join(askImagesDir(askID), hex.EncodeToString(sum[:])+strings.ToLower(path.Ext(imagePath)))
}

func writeAskImages(askID string, images []askImage) error {
	for _, image := range images {
		if err := writeAskImage(askID, image); err != nil {
			return errors.Join(err, removeAskImages(askID))
		}
	}
	return nil
}

func writeAskImage(askID string, image askImage) error {
	if err := os.MkdirAll(askImagesDir(askID), 0o700); err != nil {
		return err
	}
	staged, err := os.CreateTemp(askImagesDir(askID), ".staged-*")
	if err != nil {
		return err
	}
	_, err = staged.Write(image.data)
	err = errors.Join(err, staged.Close())
	if err == nil {
		err = os.Rename(staged.Name(), askImageFile(askID, image.path))
	}
	if err != nil {
		return errors.Join(err, os.Remove(staged.Name()))
	}
	return nil
}

func removeAskImages(askID string) error {
	return os.RemoveAll(askImagesDir(askID))
}

func (d *Daemon) handleAskImage(client *wsClient, msg *protocol.AskImageMessage) {
	result := protocol.AskImageResultMessage{Event: protocol.EventAskImageResult, RequestID: msg.RequestID}
	mimeType, data, err := d.askImage(strings.TrimSpace(msg.AskID), msg.Path, client.selectedProfile())
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
		result.MimeType = protocol.Ptr(mimeType)
		result.DataBase64 = protocol.Ptr(base64.StdEncoding.EncodeToString(data))
	}
	d.sendToClient(client, result)
}

func (d *Daemon) askImage(askID, imagePath, profileID string) (string, []byte, error) {
	stored, err := d.store.GetAsk(askID)
	if errors.Is(err, store.ErrAskGone) {
		return "", nil, fmt.Errorf("ask %s is gone", askID)
	}
	if err != nil {
		return "", nil, err
	}
	shown, err := d.readAsk(stored)
	if err != nil {
		return "", nil, err
	}
	if !askShownIn(profileID, shown.profileID) {
		return "", nil, fmt.Errorf("ask %s is gone", askID)
	}
	mimeType, ok := ask.ImageType(imagePath)
	if !ok {
		return "", nil, fmt.Errorf("%s is not a supported image type", imagePath)
	}
	data, err := os.ReadFile(askImageFile(askID, imagePath))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil, fmt.Errorf("ask %s holds no copy of %s", askID, imagePath)
	}
	return mimeType, data, err
}

func (d *Daemon) copyImagesOfAsksOpenedBeforeImagesTravelled() {
	asks, err := d.store.Asks()
	if err != nil {
		d.logf("copy images of earlier asks: %v", err)
		return
	}
	for _, stored := range asks {
		parts, err := ask.Parse(stored.Markdown)
		if err != nil {
			continue
		}
		for _, imagePath := range ask.ImagePaths(parts, stored.BaseDir) {
			if _, err := os.Stat(askImageFile(stored.ID, imagePath)); !errors.Is(err, os.ErrNotExist) {
				continue
			}
			data, err := os.ReadFile(imagePath)
			if err != nil {
				continue
			}
			if err := writeAskImage(stored.ID, askImage{path: imagePath, data: data}); err != nil {
				d.logf("copy image %s of ask %s: %v", imagePath, stored.ID, err)
			}
		}
	}
}
