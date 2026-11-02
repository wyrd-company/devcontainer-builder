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
		executable, err := hostExecutable(args[0], opts.Workspace, opts.Env)
		if err != nil {
			return fmt.Errorf("the initializeCommand in the devcontainer.json failed: %w", err)
		}
		cmd := exec.CommandContext(ctx, executable, args[1:]...)
		// The resolver already selected the child executable. Avoid a second
		// lookup of a bare name through the parent environment.
		cmd.Path, cmd.Err = executable, nil
		cmd.Args[0] = args[0]
		cmd.Dir, cmd.Env, cmd.Stdin = opts.Workspace, opts.Env, opts.Stdin
		var buffered bytes.Buffer
		writer := writerFunc(func(p []byte) (int, error) { output(p); return len(p), nil })
		if name != "" {
			cmd.Stdout, cmd.Stderr = &buffered, &buffered
		} else {
			cmd.Stdout, cmd.Stderr = writer, writer
		}
		err = cmd.Run()
		if runtime.GOOS != "windows" && errors.Is(err, syscall.ENOEXEC) {
			fallback := exec.CommandContext(ctx, "/bin/sh", append([]string{executable}, args[1:]...)...)
			fallback.Dir, fallback.Env, fallback.Stdin = cmd.Dir, cmd.Env, cmd.Stdin
			fallback.Stdout, fallback.Stderr = cmd.Stdout, cmd.Stderr
			err = fallback.Run()
		}
		if name != "" {
			output(buffered.Bytes())
		}
		if err == nil {
			return nil
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && (exit.ExitCode() == 130 || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() == syscall.SIGINT) {
			output([]byte("initializeCommand interrupted.\n"))
			return errInterrupted
		}
		return fmt.Errorf("the initializeCommand in the devcontainer.json failed: %w", err)
	}
	if len(command.Parallel) == 0 {
		err := run(command, "")
		if errors.Is(err, errInterrupted) {
			return nil
		}
		return err
	}
	results := make(chan error, len(command.Parallel))
	for name, child := range command.Parallel {
		go func() { results <- run(child, name) }()
	}
	for range command.Parallel {
		if err := <-results; err != nil {
			if errors.Is(err, errInterrupted) {
				return nil
			}
			return err
		}
	}
	return nil
}

var errInterrupted = errors.New("initializeCommand interrupted")

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// hostExecutable resolves argv against the child environment and directory.
// exec.CommandContext otherwise uses the parent's PATH and resolves relative
// PATH entries before Cmd.Dir takes effect.
func hostExecutable(name, workspace string, env []string) (string, error) {
	dir, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(name) {
		return name, nil
	}
	if strings.ContainsAny(name, `/\`) {
		if runtime.GOOS != "windows" {
			return name, nil
		}
		return filepath.Join(dir, name), nil
	}
	if env == nil {
		env = os.Environ()
	}
	value := func(key string) (string, bool) {
		for i := len(env) - 1; i >= 0; i-- {
			k, v, ok := strings.Cut(env[i], "=")
			if ok && (k == key || runtime.GOOS == "windows" && strings.EqualFold(k, key)) {
				return v, true
			}
		}
		return "", false
	}
	path, found := value("PATH")
	if !found && runtime.GOOS != "windows" {
		path = "/usr/bin:/bin"
	}
	suffixes := []string{""}
	if runtime.GOOS == "windows" && filepath.Ext(name) == "" {
		extensions, ok := value("PATHEXT")
		if !ok {
			extensions = ".COM;.EXE;.BAT;.CMD"
		}
		suffixes = strings.Split(extensions, ";")
	}
	// SplitList drops an empty string; execvp treats empty PATH as the cwd.
	entries := filepath.SplitList(path)
	if path == "" {
		entries = []string{""}
	}
	for _, entry := range entries {
		originalEntry := entry
		if !filepath.IsAbs(entry) {
			entry = filepath.Join(dir, entry)
		}
		for _, suffix := range suffixes {
			candidate := filepath.Join(entry, name+suffix)
			info, err := os.Stat(candidate)
			if err == nil && !info.IsDir() && (runtime.GOOS == "windows" || info.Mode()&0111 != 0) {
				if runtime.GOOS != "windows" {
					return filepath.Join(originalEntry, name+suffix), nil
				}
				return candidate, nil
			}
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}
