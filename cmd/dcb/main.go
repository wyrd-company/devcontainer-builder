// Command dcb builds and manages development containers through the Engine API.
// relationships:
//
//	references: [CONTRIBUTING]
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/wyrd-company/devcontainer-builder/pkg/cli"
)

// Version is overridden at link time via -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], Version, os.Stdin, os.Stdout, os.Stderr)
	cancel()
	os.Exit(code)
}
