package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

const bridgeForward = "forward"
const forwardComplete = "forward-complete"

type forwardRequest struct {
	Frame  json.RawMessage    `json:"frame"`
	Caller protocol.SessionID `json:"caller"`
}

type bufferedSocket struct {
	net.Conn
	reader *bufio.Reader
}

func (c bufferedSocket) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (r *Router) Forward(socket net.Conn, frame []byte, caller protocol.SessionID) {
	d := r.daemon
	conn, name, err := d.homeConn(d.enrollmentStatus())
	if err != nil {
		d.sendError(socket, err.Error())
		return
	}
	lost := fmt.Sprintf("lost %s during this call; it may have run", name)
	header, err := json.Marshal(forwardRequest{Frame: frame, Caller: caller})
	if err != nil {
		d.sendError(socket, err.Error())
		return
	}
	channel, requests, err := conn.Open(d.life.Context(), bridgeForward, header)
	if err != nil {
		var refused *bridge.CallRejected
		if errors.As(err, &refused) {
			d.sendError(socket, err.Error())
		} else {
			d.sendError(socket, lost)
		}
		return
	}
	defer channel.Close()
	completed := make(chan bool, 1)
	goTransport(func() {
		done := false
		for request := range requests {
			if request.Type == forwardComplete {
				done = true
			}
			if request.WantReply {
				_ = request.Reply(request.Type == forwardComplete, nil)
			}
		}
		completed <- done
	})
	inputEnded := make(chan struct{})
	goTransport(func() {
		defer close(inputEnded)
		_, _ = io.Copy(channel, socket)
		_ = channel.CloseWrite()
	})
	defer func() {
		_ = channel.Close()
		_ = socket.SetReadDeadline(time.Now())
		<-inputEnded
	}()
	reader := bufio.NewReader(channel)
	for {
		frame, err := reader.ReadBytes('\n')
		if len(frame) > 0 && frame[len(frame)-1] == '\n' {
			if _, writeErr := socket.Write(frame); writeErr != nil {
				return
			}
		}
		if err != nil {
			_ = channel.Close()
			if !<-completed {
				d.sendError(socket, lost)
			}
			return
		}
	}
}

func (d *Daemon) forwardBridgeHandler(host string) func(*bridge.Conn, ssh.NewChannel) {
	return func(conn *bridge.Conn, incoming ssh.NewChannel) {
		if !d.life.Go("forwardConnection", func() {
			var request forwardRequest
			if err := json.Unmarshal(incoming.ExtraData(), &request); err != nil {
				_ = incoming.Reject(ssh.Prohibited, err.Error())
				return
			}
			if !d.isOutpostConn(host, conn) {
				_ = incoming.Reject(ssh.Prohibited, "outpost bridge was replaced")
				return
			}
			row, err := d.resolveOutpost(host)
			if err != nil {
				_ = incoming.Reject(ssh.Prohibited, err.Error())
				return
			}
			server, relay, err := socketPair()
			if err != nil {
				_ = incoming.Reject(ssh.ResourceShortage, err.Error())
				return
			}
			defer server.Close()
			defer relay.Close()
			channel, requests, err := incoming.Accept()
			if err != nil {
				return
			}
			defer channel.Close()
			goTransport(func() { ssh.DiscardRequests(requests) })
			stop := context.AfterFunc(conn.Context(), func() { _ = relay.Close() })
			defer stop()
			inputEnded := make(chan struct{})
			goTransport(func() {
				defer close(inputEnded)
				if _, err := relay.Write(request.Frame); err == nil {
					_, _ = io.Copy(relay, channel)
				}
				_ = relay.CloseWrite()
			})
			outputEnded := make(chan struct{})
			goTransport(func() {
				defer close(outputEnded)
				_, _ = io.Copy(channel, relay)
			})
			ctx := context.WithValue(d.life.Context(), originKey{}, Origin{Host: host, Name: row.Name, Caller: request.Caller, bridge: conn})
			d.handleConnectionContext(ctx, server)
			<-outputEnded
			if conn.Context().Err() == nil {
				_, _ = channel.SendRequest(forwardComplete, true, nil)
			}
			_ = channel.CloseWrite()
			_ = channel.Close()
			_ = relay.Close()
			<-inputEnded
		}) {
			_ = incoming.Reject(ssh.ResourceShortage, "daemon is stopping")
		}
	}
}

func socketPair() (*net.UnixConn, *net.UnixConn, error) {
	syscall.ForkLock.RLock()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, nil, err
	}
	pausepoint.At(pausepoint.ForwardSocketCreated)
	files := []*os.File{os.NewFile(uintptr(fds[0]), "forward-server"), os.NewFile(uintptr(fds[1]), "forward-relay")}
	defer files[0].Close()
	defer files[1].Close()
	server, err := net.FileConn(files[0])
	if err != nil {
		return nil, nil, err
	}
	relay, err := net.FileConn(files[1])
	if err != nil {
		_ = server.Close()
		return nil, nil, err
	}
	return server.(*net.UnixConn), relay.(*net.UnixConn), nil
}

func (d *Daemon) callerSession(ctx context.Context, frame []byte) protocol.SessionID {
	if origin, remote := OriginOf(ctx); remote {
		return origin.Caller
	}
	_, terminal := socketFrameCaller(frame)
	return d.sessionInTerminal(terminal)
}
