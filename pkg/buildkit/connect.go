package buildkit

import (
	"context"
	"fmt"
	"net"
	"os"

	dockerclient "github.com/docker/docker/client"
	"github.com/moby/buildkit/client"
)

// Connection is a BuildKit client together with the Docker Engine API
// client used to reach the daemon.
//
// When the connection dials the Docker daemon's built-in BuildKit
// (the default), Daemon is true: results are exported straight into
// the daemon's image store and image references resolve against local
// images first. When it dials a separate BuildKit (BUILDKIT_HOST or an
// explicit address), Daemon is false and loaded images are streamed to
// the Engine API image-load endpoint instead.
type Connection struct {
	// BuildKit is the BuildKit control client.
	BuildKit *client.Client
	// Docker is the Engine API client. It is always set: it carries the
	// dial for the daemon's BuildKit, and image loads for a separate one.
	Docker *dockerclient.Client
	// Address is the BuildKit address dialed, or empty for the daemon's
	// built-in BuildKit.
	Address string
	// Daemon reports whether BuildKit is the Docker daemon's own.
	Daemon bool

	ownDocker bool
}

// DefaultAddress returns the BuildKit address to dial when none is
// provided: the BUILDKIT_HOST environment variable, or empty, which
// selects the Docker daemon's built-in BuildKit.
func DefaultAddress() string {
	return os.Getenv("BUILDKIT_HOST")
}

// Connect opens a BuildKit connection.
//
// The address is taken from addr, then BUILDKIT_HOST. When both are
// empty, Connect dials the Docker daemon's built-in BuildKit through the
// Engine API (the "/grpc" and "/session" upgrade endpoints, as
// `docker buildx` does for its default "docker" driver), so stock Docker
// needs no extra setup.
//
// docker is the Engine API client to use; when nil, one is created from
// the standard DOCKER_* environment and closed with the Connection.
func Connect(ctx context.Context, addr string, docker *dockerclient.Client) (*Connection, error) {
	conn := &Connection{Docker: docker}
	if conn.Docker == nil {
		dc, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
		if err != nil {
			return nil, fmt.Errorf("docker client: %w", err)
		}
		conn.Docker = dc
		conn.ownDocker = true
	}

	if addr == "" {
		addr = DefaultAddress()
	}
	var (
		bk  *client.Client
		err error
	)
	if addr == "" {
		bk, err = client.New(ctx, "", daemonDialers(conn.Docker)...)
		conn.Daemon = true
	} else {
		bk, err = client.New(ctx, addr)
		conn.Address = addr
	}
	if err != nil {
		conn.closeDocker()
		return nil, fmt.Errorf("buildkit connect %s: %w", conn.Describe(), err)
	}
	conn.BuildKit = bk
	return conn, nil
}

// daemonDialers routes the BuildKit control API and the client session
// through the Docker Engine API's hijacked upgrade endpoints.
func daemonDialers(docker *dockerclient.Client) []client.ClientOpt {
	return []client.ClientOpt{
		client.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return docker.DialHijack(ctx, "/grpc", "h2c", nil)
		}),
		client.WithSessionDialer(func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
			return docker.DialHijack(ctx, "/session", proto, meta)
		}),
	}
}

// Describe names the BuildKit endpoint for logs and errors.
func (c *Connection) Describe() string {
	if c.Address != "" {
		return c.Address
	}
	return "docker daemon (" + c.Docker.DaemonHost() + ")"
}

// Close releases the BuildKit client and, when Connect created it, the
// Engine API client.
func (c *Connection) Close() error {
	var err error
	if c.BuildKit != nil {
		err = c.BuildKit.Close()
	}
	c.closeDocker()
	return err
}

func (c *Connection) closeDocker() {
	if c.ownDocker && c.Docker != nil {
		_ = c.Docker.Close()
	}
}
