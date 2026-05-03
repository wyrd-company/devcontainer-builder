package kubernetes

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

// PodInfo is a small projection of corev1.Pod's status. Returned by
// Inspect; sufficient for the most common embedder needs without
// forcing them to import corev1 just to ask "is it running?".
type PodInfo struct {
	Namespace     string
	Name          string
	UID           string
	NodeName      string
	Phase         corev1.PodPhase
	PodIP         string
	Ready         bool
	StartedAt     string
	CreatedAt     string
	Labels        map[string]string
	Annotations   map[string]string
	ContainerName string
	Image         string
}

// Inspect returns the current state of the devcontainer for the given
// local workspace path, scoped to ns (or the host's default
// namespace). Returns ErrPodNotFound if no pod matches the workspace
// label.
func (h *Host) Inspect(ctx context.Context, ns, localWorkspace string) (*PodInfo, error) {
	if localWorkspace == "" {
		return nil, errorf("Inspect: localWorkspace is required")
	}
	ns = h.namespaceOr(ns)
	pod, err := h.findExisting(ctx, ns, LocalFolderHash(localWorkspace))
	if err != nil {
		return nil, err
	}
	if pod == nil {
		return nil, ErrPodNotFound
	}
	return podToInfo(pod), nil
}

// InspectByName returns the PodInfo for an exact namespace+name pair.
// Useful when the embedder already has the pod's identity (from Up).
func (h *Host) InspectByName(ctx context.Context, ns, name string) (*PodInfo, error) {
	ns = h.namespaceOr(ns)
	pod, err := h.cs.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ErrPodNotFound
		}
		return nil, errorf("get pod: %w", err)
	}
	return podToInfo(pod), nil
}

// Delete removes the devcontainer pod for a given workspace. Returns
// ErrPodNotFound if no pod exists. Pods on Kubernetes don't have a
// docker-style "stop without remove" semantic — Delete subsumes both.
func (h *Host) Delete(ctx context.Context, ns, localWorkspace string) error {
	if localWorkspace == "" {
		return errorf("Delete: localWorkspace is required")
	}
	ns = h.namespaceOr(ns)
	pod, err := h.findExisting(ctx, ns, LocalFolderHash(localWorkspace))
	if err != nil {
		return err
	}
	if pod == nil {
		return ErrPodNotFound
	}
	return h.DeleteByName(ctx, ns, pod.Name)
}

// DeleteByName removes a pod by exact namespace+name pair. Returns nil
// if the pod is already gone.
func (h *Host) DeleteByName(ctx context.Context, ns, name string) error {
	ns = h.namespaceOr(ns)
	opts := metav1.DeleteOptions{}
	if h.opts.DeleteGracePeriod > 0 {
		secs := int64(h.opts.DeleteGracePeriod.Seconds())
		opts.GracePeriodSeconds = &secs
	}
	if err := h.cs.CoreV1().Pods(ns).Delete(ctx, name, opts); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return errorf("delete pod: %w", err)
	}
	return nil
}

// findExisting returns the most recently created (by
// CreationTimestamp) pod matching the local-folder-hash label, or nil
// if none exists.
func (h *Host) findExisting(ctx context.Context, ns, hash string) (*corev1.Pod, error) {
	if hash == "" {
		return nil, errorf("findExisting: empty hash")
	}
	list, err := h.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", LabelLocalFolderHash, hash),
	})
	if err != nil {
		return nil, errorf("list pods: %w", err)
	}
	if len(list.Items) == 0 {
		return nil, nil
	}
	// Prefer the most recently created pod. Stale terminating pods
	// can linger; the newest one is the live target.
	newest := &list.Items[0]
	for i := 1; i < len(list.Items); i++ {
		if list.Items[i].CreationTimestamp.After(newest.CreationTimestamp.Time) {
			newest = &list.Items[i]
		}
	}
	// Defensive copy: List shares pointers into a slice we don't own.
	out := *newest
	return &out, nil
}

// readyState carries the bits Up needs after waiting on Ready: the
// per-container StartedAt timestamp for the WhenStart marker.
type readyState struct {
	StartedAt string
}

// waitContainerReady polls the pod until the named container reports
// Ready=true (or the context / timeout expires). Returns the
// container's started-at time so WhenStart markers can be stable
// across restarts.
func (h *Host) waitContainerReady(ctx context.Context, ns, name, containerName string, timeout time.Duration) (*readyState, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var got readyState
	err := wait.PollUntilContextCancel(pollCtx, time.Second, true, func(ctx context.Context) (bool, error) {
		pod, err := h.cs.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, errorf("get during wait: %w", err)
		}
		switch pod.Status.Phase {
		case corev1.PodFailed:
			return false, errorf("pod %s/%s entered Failed phase: %s", ns, name, pod.Status.Reason)
		case corev1.PodSucceeded:
			return false, errorf("pod %s/%s entered Succeeded phase before lifecycle could run", ns, name)
		}
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name != containerName {
				continue
			}
			if cs.State.Terminated != nil {
				return false, errorf("container %s terminated before Ready: %s", containerName, cs.State.Terminated.Reason)
			}
			if cs.Ready {
				if cs.State.Running != nil && !cs.State.Running.StartedAt.IsZero() {
					got.StartedAt = cs.State.Running.StartedAt.UTC().Format(time.RFC3339Nano)
				} else if pod.Status.StartTime != nil {
					got.StartedAt = pod.Status.StartTime.UTC().Format(time.RFC3339Nano)
				}
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("kubernetes host: wait for container %s ready: %w", containerName, err)
	}
	if got.StartedAt == "" {
		// No StartedAt observed (e.g. fake clientsets, exotic statuses):
		// fall back to "now" so the WhenStart marker is at least stable
		// for the duration of this Up call.
		got.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return &got, nil
}

func podToInfo(p *corev1.Pod) *PodInfo {
	info := &PodInfo{
		Namespace:   p.Namespace,
		Name:        p.Name,
		UID:         string(p.UID),
		NodeName:    p.Spec.NodeName,
		Phase:       p.Status.Phase,
		PodIP:       p.Status.PodIP,
		CreatedAt:   p.CreationTimestamp.UTC().Format(time.RFC3339Nano),
		Labels:      cloneStringMap(p.Labels),
		Annotations: cloneStringMap(p.Annotations),
	}
	if p.Status.StartTime != nil {
		info.StartedAt = p.Status.StartTime.UTC().Format(time.RFC3339Nano)
	}
	if len(p.Spec.Containers) > 0 {
		info.ContainerName = p.Spec.Containers[0].Name
		info.Image = p.Spec.Containers[0].Image
	}
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Name == info.ContainerName {
			info.Ready = cs.Ready
		}
	}
	return info
}
