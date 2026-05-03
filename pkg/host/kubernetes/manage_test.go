package kubernetes

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func newPod(ns, name, hash string, created time.Time) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         ns,
			Name:              name,
			CreationTimestamp: metav1.NewTime(created),
			Labels: map[string]string{
				LabelLocalFolderHash: hash,
			},
			Annotations: map[string]string{
				AnnotationLocalFolder: "/host/" + name,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "devcontainer", Image: "alpine:3"}},
		},
	}
}

func TestInspect_NotFound(t *testing.T) {
	cs := fake.NewSimpleClientset()
	h := New(cs, nil, Options{})
	if _, err := h.Inspect(context.Background(), "default", "/host/missing"); !errors.Is(err, ErrPodNotFound) {
		t.Errorf("expected ErrPodNotFound, got %v", err)
	}
}

func TestInspect_FindsByHash(t *testing.T) {
	pod := newPod("default", "alice", LocalFolderHash("/host/alice"), time.Now())
	cs := fake.NewSimpleClientset(pod)
	h := New(cs, nil, Options{})

	info, err := h.Inspect(context.Background(), "default", "/host/alice")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Name != "alice" {
		t.Errorf("Name = %q, want alice", info.Name)
	}
	if info.ContainerName != "devcontainer" {
		t.Errorf("ContainerName = %q", info.ContainerName)
	}
	if info.Image != "alpine:3" {
		t.Errorf("Image = %q", info.Image)
	}
}

// When two pods carry the same workspace label (e.g. an old pod is
// terminating while a new one comes up) findExisting must pick the
// most recent one, otherwise Up would attach lifecycle commands to a
// dying container.
func TestFindExisting_PrefersNewest(t *testing.T) {
	now := time.Now()
	old := newPod("default", "old", LocalFolderHash("/host/proj"), now.Add(-1*time.Hour))
	fresh := newPod("default", "fresh", LocalFolderHash("/host/proj"), now)
	cs := fake.NewSimpleClientset(old, fresh)
	h := New(cs, nil, Options{})

	info, err := h.Inspect(context.Background(), "default", "/host/proj")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Name != "fresh" {
		t.Errorf("expected newest pod, got %q", info.Name)
	}
}

func TestInspectByName(t *testing.T) {
	cs := fake.NewSimpleClientset(newPod("dev", "alice", "h", time.Now()))
	h := New(cs, nil, Options{Namespace: "dev"})

	info, err := h.InspectByName(context.Background(), "", "alice")
	if err != nil {
		t.Fatalf("InspectByName: %v", err)
	}
	if info.Namespace != "dev" || info.Name != "alice" {
		t.Errorf("got %s/%s", info.Namespace, info.Name)
	}

	if _, err := h.InspectByName(context.Background(), "", "missing"); !errors.Is(err, ErrPodNotFound) {
		t.Errorf("expected ErrPodNotFound, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	pod := newPod("default", "alice", LocalFolderHash("/host/alice"), time.Now())
	cs := fake.NewSimpleClientset(pod)
	h := New(cs, nil, Options{})

	if err := h.Delete(context.Background(), "default", "/host/alice"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := h.Inspect(context.Background(), "default", "/host/alice"); !errors.Is(err, ErrPodNotFound) {
		t.Errorf("after Delete: expected ErrPodNotFound, got %v", err)
	}
}

func TestDelete_NotFound(t *testing.T) {
	cs := fake.NewSimpleClientset()
	h := New(cs, nil, Options{})
	if err := h.Delete(context.Background(), "default", "/host/missing"); !errors.Is(err, ErrPodNotFound) {
		t.Errorf("expected ErrPodNotFound, got %v", err)
	}
}

// DeleteByName tolerates an already-gone pod so embedders can call it
// as a teardown step without first checking existence.
func TestDeleteByName_AlreadyGone(t *testing.T) {
	cs := fake.NewSimpleClientset()
	h := New(cs, nil, Options{})
	if err := h.DeleteByName(context.Background(), "default", "missing"); err != nil {
		t.Errorf("expected nil for already-gone pod, got %v", err)
	}
}
