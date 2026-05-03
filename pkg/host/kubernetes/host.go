package kubernetes

import (
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Discovery surface. The annotations come from
// pkg/projection/kubernetes (path values aren't valid label values),
// and the imperative host adds a label whose value is a stable hash of
// the LocalWorkspace so a List call with a label selector can cheaply
// rediscover the pod.
const (
	// AnnotationLocalFolder mirrors pkg/host/docker.LabelLocalFolder
	// and pkg/projection/kubernetes.AnnotationLocalFolder. Stored as
	// an annotation because path values can't be label values.
	AnnotationLocalFolder = "devcontainer.local_folder"
	// AnnotationConfigFile mirrors pkg/host/docker.LabelConfigFile.
	AnnotationConfigFile = "devcontainer.config_file"
	// LabelLocalFolderHash holds the discovery key. The value is a
	// truncated sha256 of the LocalWorkspace path, so repeated calls
	// with the same workspace land on the same pod even across
	// process restarts.
	LabelLocalFolderHash = "devcontainer.io/local-folder-hash"
)

// ErrPodNotFound is returned by Inspect / Delete / Stop when no pod
// matches the supplied selector.
var ErrPodNotFound = errors.New("kubernetes host: pod not found")

// Options tunes a Host. All fields are optional.
type Options struct {
	// Logger receives informational messages. nil discards.
	Logger func(format string, args ...any)
	// Stdout / Stderr default to nil (silent). Lifecycle command output
	// is written to these unless the per-call request overrides.
	Stdout io.Writer
	Stderr io.Writer
	// Namespace is the default namespace for Up / Inspect / Delete /
	// Exec when the per-call request leaves it empty. Empty here means
	// "default".
	Namespace string
	// ReadyTimeout bounds Up's wait for the pod to reach Ready before
	// running lifecycle commands. Defaults to 5 minutes.
	ReadyTimeout time.Duration
	// DeleteGracePeriod bounds Delete's terminationGracePeriodSeconds
	// override. Defaults to the pod's own
	// TerminationGracePeriodSeconds (typically 30s).
	DeleteGracePeriod time.Duration
}

// Host is the per-cluster connection. Safe for concurrent use.
type Host struct {
	cs   kubernetes.Interface
	cfg  *rest.Config
	opts Options
}

// New wraps an existing clientset and rest.Config. Both lifetimes are
// the caller's responsibility; New does not copy or mutate either.
//
// cfg may be nil when the embedder only needs Up/Inspect/Delete (no
// exec, no port-forward); attempts to Exec or PortForward without a
// cfg return an error rather than panicking.
func New(cs kubernetes.Interface, cfg *rest.Config, opts Options) *Host {
	if opts.ReadyTimeout == 0 {
		opts.ReadyTimeout = 5 * time.Minute
	}
	return &Host{cs: cs, cfg: cfg, opts: opts}
}

// Clientset returns the underlying clientset for ad-hoc calls.
func (h *Host) Clientset() kubernetes.Interface { return h.cs }

// RESTConfig returns the underlying rest.Config (may be nil).
func (h *Host) RESTConfig() *rest.Config { return h.cfg }

func (h *Host) logf(format string, args ...any) {
	if h.opts.Logger != nil {
		h.opts.Logger(format, args...)
	}
}

func (h *Host) stdoutOr(w io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return h.opts.Stdout
}

func (h *Host) stderrOr(w io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return h.opts.Stderr
}

func (h *Host) namespaceOr(ns string) string {
	if ns != "" {
		return ns
	}
	if h.opts.Namespace != "" {
		return h.opts.Namespace
	}
	return "default"
}

func errorf(format string, args ...any) error {
	return fmt.Errorf("kubernetes host: "+format, args...)
}

// LocalFolderHash returns the discovery label value for a workspace
// path. Public so embedders that want to look up pods by the same key
// (without going through Inspect) can compute the value directly.
//
// The result is the lower-case base32 (RFC 4648, without padding) of
// the first 20 bytes of sha256(path), giving 32 characters — well
// inside Kubernetes' 63-char label-value limit and DNS-1123-friendly.
func LocalFolderHash(localWorkspace string) string {
	if localWorkspace == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(localWorkspace))
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:20])
	return strings.ToLower(enc)
}
