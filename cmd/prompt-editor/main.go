package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

//go:embed web
var web embed.FS

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(runCLI(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const (
	serverRecord = ".prompt-editor/server.json"
	serverLock   = ".prompt-editor/server.lock"
)

var errEditorRunning = errors.New("an editor is already serving this checkout")

func claimCheckout(repo string) (*os.File, error) {
	root, err := os.OpenRoot(repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Dir(serverLock), 0700); err != nil {
		return nil, err
	}
	lock, err := root.OpenFile(serverLock, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errEditorRunning
		}
		return nil, err
	}
	return lock, nil
}

func recordServer(repo, address string) error {
	root, err := os.OpenRoot(repo)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Dir(serverRecord), 0700); err != nil {
		return err
	}
	return writeJSON(root, serverRecord, map[string]string{"url": address, "repo": repo})
}

func serve(ctx context.Context, options cliOptions, out io.Writer) error {
	editor, err := openEditor(options.repo)
	if err != nil {
		return err
	}
	defer editor.root.Close()
	claim, err := claimCheckout(editor.repo)
	if errors.Is(err, errEditorRunning) {
		running, err := editor.show(ctx, "", "", false)
		if err != nil {
			return fmt.Errorf("%w: %w", errEditorRunning, err)
		}
		fmt.Fprintf(out, "Prompt editor already running: %s\n", running.(map[string]any)["url"])
		return nil
	}
	if err != nil {
		return err
	}
	defer claim.Close()
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", options.port))
	if err != nil {
		return err
	}
	defer listener.Close()
	assets, err := fs.Sub(web, "web")
	if err != nil {
		return err
	}
	editor.assets = http.FileServer(http.FS(assets))
	editor.host = listener.Addr().String()
	editor.defaultBase = options.base
	address := "http://" + editor.host
	if err := recordServer(editor.repo, address); err != nil {
		return err
	}
	fmt.Fprintf(out, "Prompt editor: %s\nEditing: %s\n", address, editor.root.Name())
	server := &http.Server{Handler: editor, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); _ = server.Close() }()
	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
