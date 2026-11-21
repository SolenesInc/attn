package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) askCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"ask": command.Typed(d.runAsk),
		"ask list": command.Typed(func(_ context.Context, c command.Caller, _ command.AskListArgs) (any, error) {
			return d.askList(c.Session)
		}),
		"ask show": command.Typed(func(_ context.Context, c command.Caller, a command.AskShowArgs) (any, error) {
			return d.askShow(c.Session, a.Ask)
		}),
		"ask close": command.Typed(func(_ context.Context, c command.Caller, a command.AskCloseArgs) (any, error) {
			return d.askClose(c.Session, a.Ask, a.Reason)
		}),
	}
}

func (d *Daemon) runAsk(_ context.Context, c command.Caller, a command.AskArgs) (any, error) {
	if strings.TrimSpace(a.Markdown) == "" {
		return nil, errors.New("pipe the ask's markdown on stdin, or pass --file")
	}
	baseDir, err := d.askBaseDir(c, a.BaseDir)
	if err != nil {
		return nil, err
	}
	parts, err := ask.Parse(a.Markdown)
	if err != nil {
		return nil, err
	}
	images, err := readAskImages(ask.ImagePaths(parts, baseDir))
	if err != nil {
		return nil, err
	}
	msg := protocol.AskOpenMessage{Cmd: protocol.CmdAskOpen, SessionID: c.Session, Markdown: a.Markdown, BaseDir: baseDir, Images: images}
	if seed := strings.TrimSpace(a.Seed); seed != "" {
		msg.SeedID = protocol.Ptr(seed)
	}
	if subject := strings.TrimSpace(a.Subject); subject != "" {
		msg.Subject = protocol.Ptr(subject)
	}
	if d.enrollmentStatus().IsHome() {
		return d.askOpen(&msg)
	}
	return d.askOpenOnHome(msg)
}

func (d *Daemon) askBaseDir(c command.Caller, given string) (string, error) {
	if given != "" {
		return given, nil
	}
	if row := d.store.Get(c.Session); row != nil && row.Directory != "" {
		return row.Directory, nil
	}
	return "", errors.New("this caller has no session folder to resolve the ask's images against; name each image by its absolute path")
}

func readAskImages(paths []string) ([]protocol.AskImage, error) {
	total := int64(0)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("the ask shows the image %s, which cannot be read: %w", path, errors.Unwrap(err))
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("the ask shows the image %s, which is not a file", path)
		}
		total += info.Size()
	}
	if total > ask.MaxImageBytes {
		return nil, fmt.Errorf("an ask's images are copied with it and hold at most max_ask_image_bytes=%d (%d MiB) together; these %d images hold %d bytes. Show fewer or smaller images",
			ask.MaxImageBytes, ask.MaxImageBytes>>20, len(paths), total)
	}
	images := make([]protocol.AskImage, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("the ask shows the image %s, which cannot be read: %w", path, err)
		}
		images = append(images, protocol.AskImage{Path: path, DataBase64: base64.StdEncoding.EncodeToString(data)})
	}
	return images, nil
}

func (d *Daemon) askOpenOnHome(msg protocol.AskOpenMessage) (protocol.Ask, error) {
	frame, err := json.Marshal(msg)
	if err != nil {
		return protocol.Ask{}, err
	}
	var out bytes.Buffer
	if err := d.router().Forward(append(frame, '\n'), msg.SessionID, nil, &out); err != nil {
		return protocol.Ask{}, err
	}
	var reply protocol.Response
	scanner := bufio.NewScanner(&out)
	scanner.Buffer(nil, protocol.MaxSocketFrameBytes)
	for scanner.Scan() {
		reply = protocol.Response{}
		if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
			return protocol.Ask{}, fmt.Errorf("the home's reply to the ask: %w", err)
		}
	}
	if !reply.Ok || reply.AskOpenResult == nil {
		return protocol.Ask{}, errors.New(protocol.Deref(reply.Error))
	}
	return *reply.AskOpenResult, nil
}
