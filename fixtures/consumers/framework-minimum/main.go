package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"

	"github.com/nxnminieye/nexa/nexactl/host"
)

func newMinimumApplication() (*host.Host, http.Handler, error) {
	composed, err := host.New(host.Options{Name: "nexactl-minimum", Version: "v0.1.0"})
	if err != nil {
		return nil, nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]bool{"ready": true})
	})
	return composed, mux, nil
}

func main() {
	listen := flag.String("listen", "127.0.0.1:0", "HTTP listen address")
	flag.Parse()
	composed, handler, err := newMinimumApplication()
	if err != nil {
		panic(err)
	}
	inspection := composed.Inspect()
	if len(inspection.Plugins) != 0 || len(inspection.Capabilities) != 0 {
		panic("minimum host unexpectedly composed optional capabilities")
	}

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		panic(err)
	}
	server := &http.Server{Handler: handler}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fmt.Println("http://" + listener.Addr().String())
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}
