package buildkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/progress/progressui"
	fsutil "github.com/tonistiigi/fsutil"
	"golang.org/x/sync/errgroup"
)

// exporterMoby is the Docker daemon's exporter: it writes the result
// straight into the daemon's image store.
const exporterMoby = "moby"

// SolveOptions describes which BuildKit to use and how to export the
// resulting image.
type SolveOptions struct {
	// Address is the BuildKit endpoint. When empty, BUILDKIT_HOST is
	// consulted; when that is empty too, the Docker daemon's built-in
	// BuildKit is used through the Engine API.
	Address string
	// Docker is the Engine API client. When nil, one is created from the
	// standard DOCKER_* environment.
	Docker *dockerclient.Client
	// LocalContextName must match the name used in the LLB graph.
	LocalContextName string
	// LocalContextDir is the host directory exposed to the build under
	// LocalContextName.
	LocalContextDir string
	// Tags name the exported image.
	Tags []string
	// Push pushes the image to the registry named by Tags.
	Push bool
	// Load puts the image into the Docker daemon's image store. With the
	// daemon's BuildKit the image is exported there directly; with a
	// separate BuildKit it is streamed to the Engine API image-load
	// endpoint.
	Load bool
	// LoadWriter, when set with Load, receives a docker-format image
	// tarball instead of the image being loaded into the daemon.
	LoadWriter io.Writer
	// OutputDir, when non-empty, requests a "local" exporter writing
	// the image filesystem to this directory.
	OutputDir string
	// CacheImports / CacheExports map directly to BuildKit's options.
	CacheImports []CacheRef
	CacheExports []CacheRef
	// Platforms is the list of target platforms. SolveDevcontainer and
	// SolveDockerfile honor all entries (producing a manifest-list image
	// for >1); the bare Solve uses none.
	Platforms []string
	// NoCache disables the build cache for every step.
	NoCache bool
	// Pull always fetches referenced images from their registry. When
	// false, the daemon's BuildKit prefers images already in the local
	// image store.
	Pull bool
	// FrontendAttrs are passed through verbatim. Useful for advanced
	// callers; normally empty.
	FrontendAttrs map[string]string
	// Progress controls the display: "auto", "tty", "plain".
	Progress string
	// Stdout / Stderr default to os.Stdout / os.Stderr.
	Stdout io.Writer
	Stderr io.Writer
}

// CacheRef is a small mirror of client.CacheOptionsEntry that's easier
// for CLI flag parsing.
type CacheRef struct {
	Type  string
	Attrs map[string]string
}

// ParseCacheRefs parses cache specs in `docker build` form
// ("type=registry,ref=...") into CacheRefs. A spec with no "=" is a
// registry reference, as `docker buildx` treats it.
func ParseCacheRefs(specs []string) []CacheRef {
	out := make([]CacheRef, 0, len(specs))
	for _, raw := range specs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "=") {
			out = append(out, CacheRef{Type: "registry", Attrs: map[string]string{"ref": raw}})
			continue
		}
		ref := CacheRef{Attrs: map[string]string{}}
		for _, kv := range strings.Split(raw, ",") {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			if k == "type" {
				ref.Type = v
				continue
			}
			ref.Attrs[k] = v
		}
		if ref.Type != "" {
			out = append(out, ref)
		}
	}
	return out
}

// Result is returned from the Solve functions.
type Result struct {
	// SolveResponse is the raw BuildKit solve response.
	SolveResponse *client.SolveResponse
}

// Solve marshals the supplied state and submits it to BuildKit.
func Solve(ctx context.Context, state llb.State, opt SolveOptions) (*Result, error) {
	localMounts, err := localContextMounts(opt)
	if err != nil {
		return nil, err
	}
	return runSolve(ctx, opt, false, localMounts, func(ctx context.Context, conn *Connection, so client.SolveOpt, ch chan *client.SolveStatus) (*client.SolveResponse, error) {
		def, err := state.Marshal(ctx, marshalOpts(opt)...)
		if err != nil {
			close(ch)
			return nil, fmt.Errorf("marshal llb: %w", err)
		}
		return conn.BuildKit.Solve(ctx, def, so, ch)
	})
}

// solveFunc submits one solve. It must close ch, as the BuildKit client
// does, including when it fails before submitting.
type solveFunc func(ctx context.Context, conn *Connection, so client.SolveOpt, ch chan *client.SolveStatus) (*client.SolveResponse, error)

// runSolve connects to BuildKit, sets up exports and progress display,
// and runs solve.
func runSolve(ctx context.Context, opt SolveOptions, multi bool, localMounts map[string]fsutil.FS, solve solveFunc) (*Result, error) {
	conn, err := Connect(ctx, opt.Address, opt.Docker)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	stderr := opt.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	exports, load, err := exportsFor(ctx, opt, conn, multi)
	if err != nil {
		return nil, err
	}

	statusCh := make(chan *client.SolveStatus)
	display, err := progressui.NewDisplay(stderr, displayMode(opt.Progress))
	if err != nil {
		return nil, fmt.Errorf("progress display: %w", err)
	}

	so := client.SolveOpt{
		Exports:       exports,
		LocalMounts:   localMounts,
		CacheImports:  cacheEntries(opt.CacheImports),
		CacheExports:  cacheEntries(opt.CacheExports),
		FrontendAttrs: opt.FrontendAttrs,
	}

	g, gctx := errgroup.WithContext(ctx)
	var resp *client.SolveResponse
	g.Go(func() error {
		var solveErr error
		resp, solveErr = solve(gctx, conn, so, statusCh)
		return solveErr
	})
	g.Go(func() error {
		_, derr := display.UpdateFrom(ctx, statusCh)
		return derr
	})
	err = g.Wait()
	if load != nil {
		if lerr := load.wait(err); err == nil && lerr != nil {
			err = fmt.Errorf("load image into docker: %w", lerr)
		}
	}
	if err != nil {
		return nil, err
	}
	return &Result{SolveResponse: resp}, nil
}

// localContextMounts registers the build context directory, if any.
func localContextMounts(opt SolveOptions) (map[string]fsutil.FS, error) {
	mounts := map[string]fsutil.FS{}
	if opt.LocalContextName != "" && opt.LocalContextDir != "" {
		fs, err := fsutil.NewFS(opt.LocalContextDir)
		if err != nil {
			return nil, fmt.Errorf("local mount %s: %w", opt.LocalContextDir, err)
		}
		mounts[opt.LocalContextName] = fs
	}
	return mounts, nil
}

func cacheEntries(refs []CacheRef) []client.CacheOptionsEntry {
	out := make([]client.CacheOptionsEntry, 0, len(refs))
	for _, r := range refs {
		out = append(out, client.CacheOptionsEntry{Type: r.Type, Attrs: r.Attrs})
	}
	return out
}

// marshalOpts returns the constraints applied to every vertex of an
// authored LLB graph.
func marshalOpts(opt SolveOptions) []llb.ConstraintsOpt {
	if opt.NoCache {
		return []llb.ConstraintsOpt{llb.IgnoreCache}
	}
	return nil
}

// resolveMode selects how image references resolve: always from the
// registry when Pull is set; otherwise the daemon's BuildKit is asked to
// prefer its local image store, as `docker buildx` asks of its "docker"
// driver.
func resolveMode(opt SolveOptions, conn *Connection) string {
	switch {
	case opt.Pull:
		return pb.AttrImageResolveModeForcePull
	case conn.Daemon:
		return pb.AttrImageResolveModePreferLocal
	default:
		return pb.AttrImageResolveModeDefault
	}
}

func llbResolveMode(mode string) llb.ResolveMode {
	switch mode {
	case pb.AttrImageResolveModeForcePull:
		return llb.ResolveModeForcePull
	case pb.AttrImageResolveModePreferLocal:
		return llb.ResolveModePreferLocal
	default:
		return llb.ResolveModeDefault
	}
}

func displayMode(s string) progressui.DisplayMode {
	switch s {
	case "plain":
		return progressui.PlainMode
	case "tty":
		return progressui.TtyMode
	default:
		return progressui.AutoMode
	}
}

// exportsFor translates SolveOptions into exporter entries for conn.
// The returned engineLoad is non-nil when the image is streamed to the
// Engine API image-load endpoint; the caller must wait on it.
func exportsFor(ctx context.Context, opt SolveOptions, conn *Connection, multi bool) ([]client.ExportEntry, *engineLoad, error) {
	if opt.Push && opt.Load {
		return nil, nil, errors.New("--push and --load are mutually exclusive")
	}
	name := strings.Join(opt.Tags, ",")
	switch {
	case opt.Push:
		if len(opt.Tags) == 0 {
			return nil, nil, errors.New("--push requires at least one tag")
		}
		typ := client.ExporterImage
		if conn.Daemon {
			typ = exporterMoby
		}
		return []client.ExportEntry{{
			Type: typ,
			Attrs: map[string]string{
				"name":           name,
				"push":           "true",
				"name-canonical": "true",
			},
		}}, nil, nil
	case opt.Load:
		if multi {
			return nil, nil, errors.New("--load is incompatible with multi-platform builds " +
				"(docker's image store only stores single-platform images)")
		}
		if len(opt.Tags) == 0 {
			return nil, nil, errors.New("--load requires at least one tag")
		}
		if opt.LoadWriter != nil {
			return []client.ExportEntry{{
				Type:   client.ExporterDocker,
				Attrs:  map[string]string{"name": name},
				Output: writerOutput(opt.LoadWriter),
			}}, nil, nil
		}
		if conn.Daemon {
			return []client.ExportEntry{{
				Type:  exporterMoby,
				Attrs: map[string]string{"name": name},
			}}, nil, nil
		}
		load := newEngineLoad(ctx, conn.Docker)
		return []client.ExportEntry{{
			Type:   client.ExporterDocker,
			Attrs:  map[string]string{"name": name},
			Output: load.output,
		}}, load, nil
	case opt.OutputDir != "":
		if err := os.MkdirAll(opt.OutputDir, 0o755); err != nil {
			return nil, nil, err
		}
		return []client.ExportEntry{{
			Type:      client.ExporterLocal,
			OutputDir: opt.OutputDir,
		}}, nil, nil
	default:
		// No exporter: the result stays in BuildKit's cache only.
		// Useful for pre-warming caches.
		return nil, nil, nil
	}
}

// writerOutput returns an exporter output that yields w regardless of
// which path BuildKit asks for.
func writerOutput(w io.Writer) func(map[string]string) (io.WriteCloser, error) {
	return func(map[string]string) (io.WriteCloser, error) {
		if wc, ok := w.(io.WriteCloser); ok {
			return wc, nil
		}
		return nopCloser{w}, nil
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// engineLoad streams a docker-format tarball from a separate BuildKit
// into the Docker daemon through the Engine API image-load endpoint.
// The load starts when BuildKit opens the exporter output.
type engineLoad struct {
	ctx    context.Context
	docker *dockerclient.Client

	once sync.Once
	pw   *io.PipeWriter
	done chan error
}

func newEngineLoad(ctx context.Context, docker *dockerclient.Client) *engineLoad {
	return &engineLoad{ctx: ctx, docker: docker, done: make(chan error, 1)}
}

func (l *engineLoad) output(map[string]string) (io.WriteCloser, error) {
	l.once.Do(func() {
		pr, pw := io.Pipe()
		l.pw = pw
		go func() {
			err := l.load(pr)
			// Unblock the exporter if the daemon stopped reading early.
			pr.CloseWithError(errOr(err, io.ErrClosedPipe))
			l.done <- err
		}()
	})
	return l.pw, nil
}

func (l *engineLoad) load(r io.Reader) error {
	resp, err := l.docker.ImageLoad(l.ctx, r, dockerclient.ImageLoadWithQuiet(true))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !resp.JSON {
		_, err := io.Copy(io.Discard, resp.Body)
		return err
	}
	return jsonmessage.DisplayJSONMessagesStream(resp.Body, io.Discard, 0, false, nil)
}

// wait returns the load outcome once the solve has returned. solveErr,
// when non-nil, aborts a load still reading the tarball.
func (l *engineLoad) wait(solveErr error) error {
	if l.pw == nil {
		if solveErr == nil {
			return errors.New("buildkit did not produce an image to load")
		}
		return nil
	}
	if solveErr != nil {
		l.pw.CloseWithError(solveErr)
	}
	return <-l.done
}

func errOr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}
