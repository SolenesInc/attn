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
	_, err = editor.withState(func(root *os.Root) (any, error) {
		return nil, writeJSON(root, "server.json", map[string]string{"url": address, "repo": editor.repo})
	})
	if err != nil {
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
