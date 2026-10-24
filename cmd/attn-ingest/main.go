package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/ingest"
)

func main() {
	if err := run(); err != nil {
		log.Printf("attn-ingest: %v", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	clickHouseURL := os.Getenv("ATTN_INGEST_CLICKHOUSE_URL")
	if clickHouseURL == "" {
		return errors.New("set ATTN_INGEST_CLICKHOUSE_URL, such as http://user:password@clickhouse:8123/?database=attn")
	}
	listen := os.Getenv("ATTN_INGEST_LISTEN")
	if listen == "" {
		listen = ":8080"
	}
	bytesPerDay, err := bytesPerDay()
	if err != nil {
		return err
	}
	server, err := ingest.New(ctx, ingest.Config{
		ClickHouseURL:  clickHouseURL,
		IPSecret:       []byte(os.Getenv("ATTN_INGEST_IP_SECRET")),
		ClientIPHeader: os.Getenv("ATTN_INGEST_CLIENT_IP_HEADER"),
		BytesPerDay:    bytesPerDay,
		Logf:           log.Printf,
	})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
	}
	served := make(chan error, 1)
	go func() { served <- httpServer.Serve(listener) }()
	fmt.Fprintf(os.Stderr, "attn-ingest listening on %s\n", listener.Addr())

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdown)
}

func bytesPerDay() (int64, error) {
	raw := os.Getenv("ATTN_INGEST_BYTES_PER_DAY")
	if raw == "" {
		return ingest.DefaultBytesPerDay, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("ATTN_INGEST_BYTES_PER_DAY must be a positive number of bytes, such as %d, got %q", int64(ingest.DefaultBytesPerDay), raw)
	}
	return value, nil
}
