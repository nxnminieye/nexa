package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/nxnminieye/nexa/generation/internal/entexec"
	"github.com/nxnminieye/nexa/generation/internal/enthelper"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	input, err := io.ReadAll(io.LimitReader(os.Stdin, entexec.MaxStdinBytes+1))
	if err != nil || len(input) > entexec.MaxStdinBytes {
		fmt.Fprintln(os.Stderr, "invalid Ent graph request")
		os.Exit(2)
	}
	output, err := enthelper.ExecuteV2(ctx, input)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ent graph projection failed")
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(output); err != nil {
		os.Exit(1)
	}
}
