package main

import (
	"context"
	"embed"
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

const serverRecord = ".prompt-editor/server.json"

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
