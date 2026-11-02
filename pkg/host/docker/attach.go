// relationships:
//   references: [up, runargs]

package docker

import (
	"context"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// startAttached opens the stream before start so early output is retained.
// Like upstream's start-event wait, it returns after start, not container exit.
// The stream lives until EOF or the caller cancels ctx; nil writers discard.
func (h *Host) startAttached(ctx context.Context, id string, cfg *container.Config) error {
	stream, err := h.client.ContainerAttach(ctx, id, container.AttachOptions{
		Stream: true, Stdin: cfg.AttachStdin, Stdout: cfg.AttachStdout, Stderr: cfg.AttachStderr,
	})
	if err != nil {
		return errorf("attach: %w", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer stream.Close()
		var err error
		if cfg.Tty {
			_, err = io.Copy(orDiscard(h.opts.Stdout), stream.Reader)
		} else {
			_, err = stdcopy.StdCopy(orDiscard(h.opts.Stdout), orDiscard(h.opts.Stderr), stream.Reader)
		}
		if err != nil && ctx.Err() == nil {
			h.logf("container stream: %v", err)
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
			stream.Close()
		case <-done:
		}
	}()
	if cfg.AttachStdin && h.opts.Stdin != nil {
		go func() { _, _ = io.Copy(stream.Conn, h.opts.Stdin); _ = stream.CloseWrite() }()
	}
	if err := h.client.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
		stream.Close()
		return errorf("start: %w", err)
	}
	return nil
}
