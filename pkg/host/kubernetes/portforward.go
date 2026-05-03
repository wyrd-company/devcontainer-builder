package kubernetes

import (
	"context"
	"io"
	"net/http"

	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// PortForwardRequest captures the inputs to a single port-forward
// session. Mirrors `kubectl port-forward pod/<name> LOCAL:REMOTE`.
type PortForwardRequest struct {
	Namespace string
	PodName   string
	// Ports is one or more "LOCAL:REMOTE" specs (e.g. "0:8080" lets
	// the kernel pick a local port for remote 8080). Same syntax as
	// kubectl port-forward.
	Ports []string
	// Addresses are the local interfaces to bind. Empty defaults to
	// ["localhost"] (matches kubectl's default).
	Addresses []string
	// Out / ErrOut receive port-forward's startup banners
	// ("Forwarding from 127.0.0.1:1234 -> 8080"). nil discards both.
	Out    io.Writer
	ErrOut io.Writer
	// Ready, when non-nil, is closed by the forwarder once it's
	// listening locally. Embedders that need to know when it's safe
	// to dial the local port should pass a channel and wait on it.
	Ready chan struct{}
}

// PortForwardSession is the handle the embedder uses to wait for
// startup and stop the forwarder.
type PortForwardSession struct {
	// LocalPorts is populated once the forwarder is Ready. Each entry
	// is "LOCAL:REMOTE" with the actual local port the kernel
	// assigned (useful for "0:N" specs).
	LocalPorts func() ([]ForwardedPort, error)
	// Stop tears the session down. Safe to call multiple times.
	Stop func()
	// Done is closed when the forwarder goroutine exits, either
	// because Stop was called or because of an unrecoverable error.
	Done <-chan error
}

// ForwardedPort is a single mapped port pair.
type ForwardedPort struct {
	Local  uint16
	Remote uint16
}

// PortForward starts a port-forwarder in a background goroutine and
// returns a session handle. The forwarder runs until Stop is called or
// ctx is cancelled. Mirrors `kubectl port-forward pod/<name>`.
func (h *Host) PortForward(ctx context.Context, req PortForwardRequest) (*PortForwardSession, error) {
	if h.cfg == nil {
		return nil, errorf("PortForward requires a non-nil rest.Config (pass one to New)")
	}
	if req.PodName == "" {
		return nil, errorf("PortForwardRequest.PodName is required")
	}
	if len(req.Ports) == 0 {
		return nil, errorf("PortForwardRequest.Ports is empty")
	}

	ns := h.namespaceOr(req.Namespace)

	transport, upgrader, err := spdy.RoundTripperFor(h.cfg)
	if err != nil {
		return nil, errorf("portforward dialer: %w", err)
	}

	u := h.cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(ns).
		Name(req.PodName).
		SubResource("portforward").
		URL()

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", u)

	stopCh := make(chan struct{})
	ready := req.Ready
	if ready == nil {
		ready = make(chan struct{})
	}
	addresses := req.Addresses
	if len(addresses) == 0 {
		addresses = []string{"localhost"}
	}

	fw, err := portforward.NewOnAddresses(
		dialer, addresses, req.Ports, stopCh, ready,
		orDiscard(req.Out), orDiscard(req.ErrOut),
	)
	if err != nil {
		return nil, errorf("portforward init: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		// Cancel local listeners if the parent ctx ends.
		go func() {
			select {
			case <-ctx.Done():
				select {
				case <-stopCh:
				default:
					close(stopCh)
				}
			case <-stopCh:
			}
		}()
		done <- fw.ForwardPorts()
	}()

	stopOnce := make(chan struct{})
	stop := func() {
		select {
		case <-stopOnce:
			return
		default:
			close(stopOnce)
		}
		select {
		case <-stopCh:
		default:
			close(stopCh)
		}
	}

	return &PortForwardSession{
		LocalPorts: func() ([]ForwardedPort, error) {
			ports, err := fw.GetPorts()
			if err != nil {
				return nil, err
			}
			out := make([]ForwardedPort, len(ports))
			for i, p := range ports {
				out[i] = ForwardedPort{Local: p.Local, Remote: p.Remote}
			}
			return out, nil
		},
		Stop: stop,
		Done: done,
	}, nil
}

