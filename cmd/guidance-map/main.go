// Command guidance-map serves the throwaway browser prototype for the typed
// guidance registry spike.
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "loopback address to listen on")
	flag.Parse()

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "guidance-map: listen: %v\n", err)
		os.Exit(1)
	}
	server := &http.Server{Handler: newHandler()}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "guidance-map: serve: %v\n", err)
		}
	}()

	fmt.Printf("guidance map: http://%s/?variant=map\n", listener.Addr())
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	if err := server.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "guidance-map: shutdown: %v\n", err)
	}
}
