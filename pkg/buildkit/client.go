package buildkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/util/progress/progressui"
	fsutil "github.com/tonistiigi/fsutil"
	"golang.org/x/sync/errgroup"
)

// SolveOptions describes how to ship the LLB graph to a buildkitd
// instance and how to export the resulting image.
type SolveOptions struct {
	// Address is the buildkitd endpoint. When empty, BUILDKIT_HOST is
	// consulted; otherwise the unix-socket default is used.
	Address string
	// LocalContextName must match the name used in the LLB graph.
	LocalContextName string
	// LocalContextDir is the host directory exposed to the build under
	// LocalContextName.
	LocalContextDir string
	// Tags applied via the image exporter (when used).
	Tags []string
	// Push requests the "image" exporter with push=true.
	Push bool
	// Load requests the "docker" exporter; the resulting tarball is
	// streamed to LoadWriter (typically piped into `docker load`).
	Load bool
	// LoadWriter receives the docker tarball when Load is true. When
	// nil, a file under WorkDir is written and reported back.
	LoadWriter io.Writer
	// OutputDir, when non-empty, requests a "local" exporter writing
	// the image filesystem to this directory.
	OutputDir string
	// CacheImports / CacheExports map directly to BuildKit's options.
	CacheImports []CacheRef
	CacheExports []CacheRef
	// Platforms is the list of target platforms (currently the LLB
	// authoring only supports a single platform; multi-arch is left
	// for future work).
	Platforms []string
	// FrontendAttrs are passed through verbatim. Useful for advanced
	// callers; normally empty.
	FrontendAttrs map[string]string
	// Progress controls the display: "auto", "tty", "plain".
	Progress string
	// Stdout / Stderr default to os.Stdout / os.Stderr.
	Stdout io.Writer
	Stderr io.Writer
	// WorkDir is used to spool intermediate outputs (e.g. the docker
	// tarball when Load is true and LoadWriter is nil).
	WorkDir string
}

// CacheRef is a small mirror of client.CacheOptionsEntry that's easier
// for CLI flag parsing.
type CacheRef struct {
	Type  string
	Attrs map[string]string
}

// Result is returned from Solve.
type Result struct {
	// LoadTarball, when set, is the path to the docker-format tarball
	// produced by Load when LoadWriter was nil.
	LoadTarball string
	// SolveResponse is the raw BuildKit solve response.
	SolveResponse *client.SolveResponse
}

// DefaultAddress returns the address to dial when none is provided.
//
// Order of precedence:
//
//  1. BUILDKIT_HOST environment variable
//  2. /run/buildkit/buildkitd.sock (host-installed daemon)
//  3. Empty — Solve will surface a clear error.
func DefaultAddress() string {
	if v := os.Getenv("BUILDKIT_HOST"); v != "" {
		return v
	}
	if _, err := os.Stat("/run/buildkit/buildkitd.sock"); err == nil {
		return "unix:///run/buildkit/buildkitd.sock"
	}
	return ""
}

// Solve marshals the supplied state and submits it to BuildKit.
func Solve(ctx context.Context, state llb.State, opt SolveOptions) (*Result, error) {
	addr := opt.Address
	if addr == "" {
		addr = DefaultAddress()
	}
	if addr == "" {
		return nil, errors.New("buildkit: no address provided and no default could be determined " +
			"(set BUILDKIT_HOST or pass --buildkit-addr)")
	}

	def, err := state.Marshal(ctx)
	if err != nil {
		return nil, fmt.Errorf("marshal llb: %w", err)
	}

	c, err := client.New(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer c.Close()

	stdout := opt.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opt.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	exports, loadTarball, err := buildExports(opt)
	if err != nil {
		return nil, err
	}

	localMounts := map[string]fsutil.FS{}
	if opt.LocalContextName != "" && opt.LocalContextDir != "" {
		fs, err := fsutil.NewFS(opt.LocalContextDir)
		if err != nil {
			return nil, fmt.Errorf("local mount %s: %w", opt.LocalContextDir, err)
		}
		localMounts[opt.LocalContextName] = fs
	}

	cacheImports := make([]client.CacheOptionsEntry, 0, len(opt.CacheImports))
	for _, ci := range opt.CacheImports {
		cacheImports = append(cacheImports, client.CacheOptionsEntry{Type: ci.Type, Attrs: ci.Attrs})
	}
	cacheExports := make([]client.CacheOptionsEntry, 0, len(opt.CacheExports))
	for _, ce := range opt.CacheExports {
		cacheExports = append(cacheExports, client.CacheOptionsEntry{Type: ce.Type, Attrs: ce.Attrs})
	}

	statusCh := make(chan *client.SolveStatus)
	display, err := progressui.NewDisplay(stderr, displayMode(opt.Progress))
	if err != nil {
		return nil, fmt.Errorf("progress display: %w", err)
	}

	g, gctx := errgroup.WithContext(ctx)
	var resp *client.SolveResponse
	g.Go(func() error {
		var solveErr error
		resp, solveErr = c.Solve(gctx, def, client.SolveOpt{
			Exports:       exports,
			LocalMounts:   localMounts,
			CacheImports:  cacheImports,
			CacheExports:  cacheExports,
			FrontendAttrs: opt.FrontendAttrs,
		}, statusCh)
		return solveErr
	})
	g.Go(func() error {
		_, derr := display.UpdateFrom(ctx, statusCh)
		return derr
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return &Result{LoadTarball: loadTarball, SolveResponse: resp}, nil
}

func displayMode(s string) progressui.DisplayMode {
	switch s {
	case "plain":
		return progressui.PlainMode
	case "tty":
		return progressui.TtyMode
	case "auto", "":
		return progressui.AutoMode
	default:
		return progressui.AutoMode
	}
}

// buildExports translates SolveOptions into a slice of ExportEntry.
func buildExports(opt SolveOptions) ([]client.ExportEntry, string, error) {
	if opt.Push && opt.Load {
		return nil, "", errors.New("--push and --load are mutually exclusive")
	}

	var exports []client.ExportEntry
	var loadPath string

	switch {
	case opt.Push:
		if len(opt.Tags) == 0 {
			return nil, "", errors.New("--push requires at least one tag")
		}
		exports = append(exports, client.ExportEntry{
			Type: client.ExporterImage,
			Attrs: map[string]string{
				"name":           joinTags(opt.Tags),
				"push":           "true",
				"name-canonical": "true",
			},
		})
	case opt.Load:
		if len(opt.Tags) == 0 {
			return nil, "", errors.New("--load requires at least one tag")
		}
		dest := opt.LoadWriter
		if dest == nil {
			if opt.WorkDir == "" {
				return nil, "", errors.New("--load needs a WorkDir or LoadWriter")
			}
			loadPath = filepath.Join(opt.WorkDir, "image.tar")
			f, err := os.Create(loadPath)
			if err != nil {
				return nil, "", err
			}
			dest = f
			// Note: we don't close here — the exporter stream owns it.
		}
		exports = append(exports, client.ExportEntry{
			Type:   client.ExporterDocker,
			Attrs:  map[string]string{"name": joinTags(opt.Tags)},
			Output: outputFunc(dest),
		})
	case opt.OutputDir != "":
		if err := os.MkdirAll(opt.OutputDir, 0o755); err != nil {
			return nil, "", err
		}
		exports = append(exports, client.ExportEntry{
			Type:      client.ExporterLocal,
			OutputDir: opt.OutputDir,
		})
	default:
		// No exporter — image is built into BuildKit's internal cache
		// only. Useful for pre-warming caches.
	}
	return exports, loadPath, nil
}

func joinTags(tags []string) string {
	return commaJoin(tags)
}

func commaJoin(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}

// outputFunc returns a filesync.FileOutputFunc that yields the supplied
// writer regardless of which path BuildKit asks for.
func outputFunc(w io.Writer) func(map[string]string) (io.WriteCloser, error) {
	return func(_ map[string]string) (io.WriteCloser, error) {
		// Wrap bare writers (which may be *os.File) in a no-op closer
		// when they don't implement io.WriteCloser themselves.
		if wc, ok := w.(io.WriteCloser); ok {
			return wc, nil
		}
		return nopCloser{w}, nil
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
