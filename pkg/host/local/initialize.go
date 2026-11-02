// Package local executes user-authored initialization commands on the host.
// ---
// relationships:
//
//	references: [../../spec/spec, ../../../reference/cli/src/spec-node/utils]
//
// ---
package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

// InitializeOptions supplies host context, never container or remote environment.
// A nil Env inherits the process environment. Output combines both child streams.
// Parallel commands buffer their output until each command completes.
type InitializeOptions struct {
	Workspace    string
	Env          []string
	Substitution substitute.Context
	Stdin        io.Reader
	Output       io.Writer
}

// Initialize runs each time its caller opens a workspace, before container work.
// Strings use the host shell, arrays execute directly, and object entries run
// concurrently. Like upstream Promise.all, a failure returns without cancelling
// siblings. SIGINT is reported as an interruption and does not fail startup.
func Initialize(ctx context.Context, command spec.Command, opts InitializeOptions) error {
	if command.IsZero() || (command.Shell != "" && strings.TrimSpace(command.Shell) == "") {
		return nil
	}
	var mu sync.Mutex
	output := func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		if opts.Output != nil {
			_, _ = opts.Output.Write(data)
		}
	}
	run := func(command spec.Command, name string) error {
		args := substitute.Apply(opts.Substitution, command.Args)
		if command.Shell != "" {
			shell := substitute.Substitute(opts.Substitution, command.Shell)
			args = []string{"/bin/sh", "-c", shell}
			if runtime.GOOS == "windows" {
				comspec := os.Getenv("ComSpec")
				if comspec == "" {
					comspec = "cmd.exe"
				}
				args = []string{comspec, "/c", shell}
			}
		} else if runtime.GOOS == "windows" && len(args) > 0 {
			args[0] = strings.ReplaceAll(args[0], "/", "\\")
		}
		if len(args) == 0 {
			return nil
		}
		// Go resolves ./program against the caller cwd before applying Cmd.Dir.
		// Resolve relative paths with separators against the workspace instead.
		executable := args[0]
		if !filepath.IsAbs(executable) && strings.ContainsAny(executable, `/\`) {
			executable = filepath.Join(opts.Workspace, executable)
		}
		cmd := exec.CommandContext(ctx, executable, args[1:]...)
		cmd.Dir, cmd.Env, cmd.Stdin = opts.Workspace, opts.Env, opts.Stdin
		var buffered bytes.Buffer
		writer := writerFunc(func(p []byte) (int, error) { output(p); return len(p), nil })
		if name != "" {
			cmd.Stdout, cmd.Stderr = &buffered, &buffered
		} else {
			cmd.Stdout, cmd.Stderr = writer, writer
		}
		err := cmd.Run()
		if name != "" {
			output(buffered.Bytes())
		}
		if err == nil {
			return nil
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && (exit.ExitCode() == 130 || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() == syscall.SIGINT) {
			output([]byte("initializeCommand interrupted.\n"))
			return nil
		}
		return fmt.Errorf("the initializeCommand in the devcontainer.json failed: %w", err)
	}
	if len(command.Parallel) == 0 {
		return run(command, "")
	}
	results := make(chan error, len(command.Parallel))
	for name, child := range command.Parallel {
		go func() { results <- run(child, name) }()
	}
	for range command.Parallel {
		if err := <-results; err != nil {
			return err
		}
	}
	return nil
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
