package buildkit

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"

	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/moby/buildkit/client"
)

// exporterMoby is the Docker daemon's exporter: it writes the result
// straight into the daemon's image store.
const exporterMoby = "moby"

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
