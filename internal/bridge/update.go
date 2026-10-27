package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/crypto/ssh"
)

const UpdateRestartStatus = 75

type UpdateTarget struct {
	Platform   string `json:"platform"`
	Executable string `json:"executable"`
}

type UpdateHeader struct {
	Update   int    `json:"update"`
	Build    Build  `json:"build"`
	Platform string `json:"platform"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Encoding string `json:"encoding"`
}

type updateReply struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type UpdateServer struct {
	Target  UpdateTarget
	Receive func(context.Context, UpdateHeader, io.Reader, func()) error
	Restart func()
}

type UpdateConn struct {
	ready *authenticatedPeer
	stop  func() bool
}

func DialUpdate(ctx context.Context, cfg DialConfig) (*UpdateConn, *Pin, error) {
	cfg.update = true
	handshakeCtx, cancel := context.WithTimeout(ctx, LostAfter)
	defer cancel()
	ready, pin, err := handshake(handshakeCtx, cfg)
	if err != nil {
		return nil, pin, err
	}
	return &UpdateConn{ready: ready, stop: context.AfterFunc(ctx, func() { _ = ready.transport.Close() })}, pin, nil
}

func (c *UpdateConn) Close() error         { c.stop(); return c.ready.transport.Close() }
func (c *UpdateConn) Target() UpdateTarget { return *c.ready.target }
func (c *UpdateConn) Peer() Identity       { return c.ready.peer }

func (c *UpdateConn) Send(ctx context.Context, build Build, path string, phase func(string)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	header := UpdateHeader{Update: 1, Build: build, Platform: c.Target().Platform, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil)), Encoding: "zstd"}
	payload, err := json.Marshal(header)
	if err != nil {
		return err
	}
	c.ready.raw.(*updateActivityConn).start()
	channel, requests, err := c.ready.transport.OpenChannel("attn-update", payload)
	if err != nil {
		return err
	}
	defer channel.Close()
	go ssh.DiscardRequests(requests)
	stop := context.AfterFunc(ctx, func() { _ = c.ready.transport.Close() })
	defer stop()
	phase("sending")
	written := make(chan error, 1)
	go func() {
		writer, err := zstd.NewWriter(channel, zstd.WithEncoderLevel(zstd.SpeedDefault), zstd.WithEncoderConcurrency(1))
		if err == nil {
			_, err = io.Copy(writer, file)
			closeErr := writer.Close()
			if err == nil {
				err = closeErr
			}
		}
		if err == nil {
			err = channel.CloseWrite()
		}
		written <- err
	}()
	defer func() { _ = channel.Close(); <-written }()
	decoder := json.NewDecoder(channel)
	for {
		var reply updateReply
		if err := decoder.Decode(&reply); err != nil {
			return err
		}
		if reply.Error != "" {
			return fmt.Errorf("outpost refused update: %s", reply.Error)
		}
		switch reply.Status {
		case "verifying":
			phase("verifying")
		case "restarting":
			phase("restarting")
			return nil
		default:
			return fmt.Errorf("unknown update reply %q", reply.Status)
		}
	}
}

func serveUpdate(ctx context.Context, transport ssh.Conn, channels <-chan ssh.NewChannel, home Build, server *UpdateServer, activity *updateActivityConn) error {
	ctx, cancel := context.WithCancel(ctx)
	closed := make(chan struct{})
	go func() { _ = transport.Wait(); cancel(); close(closed) }()
	defer func() { _ = transport.Close(); <-closed; cancel() }()
	for incoming := range channels {
		if incoming.ChannelType() != "attn-update" {
			_ = incoming.Reject(ssh.UnknownChannelType, "update-mode connection accepts only attn-update")
			continue
		}
		activity.start()
		channel, requests, err := incoming.Accept()
		if err != nil {
			return err
		}
		defer channel.Close()
		go ssh.DiscardRequests(requests)
		encoder := json.NewEncoder(channel)
		var header UpdateHeader
		err = json.Unmarshal(incoming.ExtraData(), &header)
		if err == nil && (header.Update != 1 || header.Encoding != "zstd" || header.Size <= 0) {
			err = fmt.Errorf("unsupported update header: update=%d encoding=%q size=%d", header.Update, header.Encoding, header.Size)
		}
		if err == nil && header.Build != home {
			err = fmt.Errorf("update header build %s differs from home's build %s", header.Build, home)
		}
		if err == nil && header.Platform != server.Target.Platform {
			err = fmt.Errorf("update platform %s differs from outpost platform %s", header.Platform, server.Target.Platform)
		}
		if err == nil {
			err = server.Receive(ctx, header, channel, func() { _ = encoder.Encode(updateReply{Status: "verifying"}) })
		}
		if err != nil {
			_ = encoder.Encode(updateReply{Status: "refused", Error: err.Error()})
			return err
		}
		_ = encoder.Encode(updateReply{Status: "restarting"})
		server.Restart()
		return nil
	}
	return fmt.Errorf("update connection closed before attn-update")
}
