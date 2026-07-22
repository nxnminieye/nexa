package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/nxnminieye/nexa/generation/internal/entexec"
	"github.com/nxnminieye/nexa/generation/internal/enthelper"
)

func main() {
	stdin, err := io.ReadAll(io.LimitReader(os.Stdin, entexec.MaxStdinBytes+1))
	if err != nil || len(stdin) > entexec.MaxStdinBytes {
		os.Exit(1)
	}
	stdout, err := enthelper.Execute(context.Background(), stdin)
	if err != nil {
		if typed, ok := err.(interface {
			Code() string
			Stage() string
			Reason() string
			Pointer() string
		}); ok {
			_, _ = fmt.Fprintf(os.Stderr, "%s|%s|%s|%s", typed.Code(), typed.Stage(), typed.Reason(), typed.Pointer())
		}
		os.Exit(1)
	}
	if len(stdout) > entexec.MaxStdoutBytes {
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(stdout); err != nil {
		os.Exit(1)
	}
}
