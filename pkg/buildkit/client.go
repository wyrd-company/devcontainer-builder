package buildkit

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	dockerclient "github.com/docker/docker/client"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/progress/progressui"
	fsutil "github.com/tonistiigi/fsutil"
	"golang.org/x/sync/errgroup"
)

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
	// Push pushes the image to the registry named by Tags. With the
	// daemon's BuildKit the image is also kept in the daemon's image
	// store, from which the Engine API pushes it.
	Push bool
	// Load puts the image into the Docker daemon's image store. With the
	// daemon's BuildKit the image is exported there directly; with a
	// separate BuildKit it is streamed to the Engine API image-load
	// endpoint.
	Load bool
	// LoadWriter, when set with Load, receives a docker-format image
	// tarball. With a separate BuildKit the image is not loaded into the
	// daemon; with the daemon's BuildKit it is also kept in the daemon's
	// image store, from which the Engine API saves it.
	LoadWriter io.Writer
	// Output is a buildx exporter specification or a local destination path.
	Output string
	// OutputWriter receives archive output when dest is "-" or omitted.
	OutputWriter io.Writer
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
	// RegistryAuth supplies registry credentials. BuildKit asks for them
	// through the client session to pull and push, and a push from the
	// daemon's image store sends them to the Engine API. When nil, the
	// docker CLI's configuration is read (registryauth.FromEnvironment).
	RegistryAuth RegistryAuth
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
	// The session and the Engine API push share one credential source.
	opt.RegistryAuth = registryAuth(opt)

	stderr := opt.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	plan, err := exportsFor(ctx, opt, conn, multi)
	if err != nil {
		return nil, err
	}

	statusCh := make(chan *client.SolveStatus)
	display, err := progressui.NewDisplay(stderr, displayMode(opt.Progress))
	if err != nil {
		return nil, fmt.Errorf("progress display: %w", err)
	}

	credentials := newSessionAuth(opt.RegistryAuth)
	so := client.SolveOpt{
		Exports:       plan.entries,
		LocalMounts:   localMounts,
		CacheImports:  cacheEntries(opt.CacheImports),
		CacheExports:  cacheEntries(opt.CacheExports),
		FrontendAttrs: opt.FrontendAttrs,
		Session:       []session.Attachable{credentials},
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
	if plan.load != nil {
		if lerr := plan.load.wait(err); err == nil && lerr != nil {
			err = fmt.Errorf("load image into docker: %w", lerr)
		}
	}
	if err == nil && plan.finish != nil {
		err = plan.finish(ctx)
	}
	err = withCredentialError(err, credentials)
	if err != nil {
		if conn.Daemon && strings.Contains(err.Error(), `exporter "oci" could not be found`) {
			return nil, fmt.Errorf("OCI export requires Docker's containerd image store or a separate BuildKit worker; the classic Docker image store does not support it: %w", err)
		}
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
