package buildkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/moby/buildkit/client"
)

// exporterMoby is the Docker daemon's exporter: it writes the result
// straight into the daemon's image store.
const exporterMoby = "moby"

// exportPlan is how a solve's result leaves BuildKit.
type exportPlan struct {
	entries []client.ExportEntry
	// load, when set, streams the result to the Engine API image-load
	// endpoint; the caller must wait on it after the solve.
	load *engineLoad
	// finish, when set, runs after a successful solve, once the result
	// is in the daemon's image store.
	finish func(ctx context.Context) error
}

// exportsFor plans the export of opt's result on conn.
//
// On the daemon's BuildKit every image export goes into the daemon's
// image store, and Push and LoadWriter are then served by the Engine
// API from the stored image. The classic store offers only the "moby"
// exporter, which ignores push and cannot write a tarball, so asking it
// to push would report success without pushing.
func exportsFor(ctx context.Context, opt SolveOptions, conn *Connection, multi bool) (*exportPlan, error) {
	if opt.Push && opt.Load {
		return nil, errors.New("--push and --load are mutually exclusive")
	}
	if opt.Output != "" {
		return specifiedExport(ctx, opt, conn, multi)
	}
	name := strings.Join(opt.Tags, ",")
	switch {
	case opt.Push:
		if len(opt.Tags) == 0 {
			return nil, errors.New("--push requires at least one tag")
		}
		if conn.Daemon {
			tags := append([]string(nil), opt.Tags...)
			auth := registryAuth(opt)
			return &exportPlan{
				entries: []client.ExportEntry{{Type: exporterMoby, Attrs: map[string]string{"name": name}}},
				finish: func(ctx context.Context) error {
					return pushTags(ctx, conn.Docker, tags, auth, progressOut(opt))
				},
			}, nil
		}
		return &exportPlan{entries: []client.ExportEntry{{
			Type: client.ExporterImage,
			Attrs: map[string]string{
				"name":           name,
				"push":           "true",
				"name-canonical": "true",
			},
		}}}, nil
	case opt.Load:
		if multi {
			return nil, errors.New("--load is incompatible with multi-platform builds " +
				"(docker's image store only stores single-platform images)")
		}
		if len(opt.Tags) == 0 {
			return nil, errors.New("--load requires at least one tag")
		}
		if opt.LoadWriter != nil && conn.Daemon {
			tags := append([]string(nil), opt.Tags...)
			return &exportPlan{
				entries: []client.ExportEntry{{Type: exporterMoby, Attrs: map[string]string{"name": name}}},
				finish: func(ctx context.Context) error {
					return saveTags(ctx, conn.Docker, tags, opt.LoadWriter)
				},
			}, nil
		}
		if opt.LoadWriter != nil {
			return &exportPlan{entries: []client.ExportEntry{{
				Type:   client.ExporterDocker,
				Attrs:  map[string]string{"name": name},
				Output: writerOutput(opt.LoadWriter),
			}}}, nil
		}
		if conn.Daemon {
			return &exportPlan{entries: []client.ExportEntry{{
				Type:  exporterMoby,
				Attrs: map[string]string{"name": name},
			}}}, nil
		}
		load := newEngineLoad(ctx, conn.Docker)
		return &exportPlan{
			entries: []client.ExportEntry{{
				Type:   client.ExporterDocker,
				Attrs:  map[string]string{"name": name},
				Output: load.output,
			}},
			load: load,
		}, nil
	case opt.OutputDir != "":
		if err := os.MkdirAll(opt.OutputDir, 0o755); err != nil {
			return nil, err
		}
		return &exportPlan{entries: []client.ExportEntry{{
			Type:      client.ExporterLocal,
			OutputDir: opt.OutputDir,
		}}}, nil
	default:
		// No exporter: the result stays in BuildKit's cache only.
		// Useful for pre-warming caches.
		return &exportPlan{}, nil
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

// progressOut is where Engine API progress for opt is written.
func progressOut(opt SolveOptions) io.Writer {
	if opt.Stderr != nil {
		return opt.Stderr
	}
	return os.Stderr
}

// pushTags pushes each tag from the daemon's image store through the
// Engine API, with the registry credentials auth supplies.
func pushTags(ctx context.Context, docker *dockerclient.Client, tags []string, auth RegistryAuth, progress io.Writer) error {
	for _, tag := range tags {
		encoded, err := auth.EngineAuth(tag)
		if err != nil {
			return fmt.Errorf("push %s: %w", tag, err)
		}
		rc, err := docker.ImagePush(ctx, tag, image.PushOptions{RegistryAuth: encoded})
		if err != nil {
			return fmt.Errorf("push %s: %w", tag, err)
		}
		err = jsonmessage.DisplayJSONMessagesStream(rc, progress, 0, false, nil)
		rc.Close()
		if err != nil {
			return fmt.Errorf("push %s: %w", tag, err)
		}
	}
	return nil
}

// saveTags writes the daemon's image tarball for tags to w through the
// Engine API, then closes w when it is a Closer, as an exporter output
// is closed.
func saveTags(ctx context.Context, docker *dockerclient.Client, tags []string, w io.Writer) error {
	rc, err := docker.ImageSave(ctx, tags)
	if err != nil {
		return fmt.Errorf("save %s: %w", strings.Join(tags, ","), err)
	}
	defer rc.Close()
	if _, err := io.Copy(w, rc); err != nil {
		return fmt.Errorf("save %s: %w", strings.Join(tags, ","), err)
	}
	if c, ok := w.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
