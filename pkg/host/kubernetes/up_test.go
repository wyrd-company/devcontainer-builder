package kubernetes

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestUp_RejectsMissingFields(t *testing.T) {
	cs := fake.NewSimpleClientset()
	h := New(cs, nil, Options{})
	ctx := context.Background()

	if _, err := h.Up(ctx, UpRequest{}); err == nil {
		t.Error("expected error for missing Spec")
	}
	if _, err := h.Up(ctx, UpRequest{Spec: &spec.DevContainer{}}); err == nil {
		t.Error("expected error for missing LocalWorkspace")
	}
	if _, err := h.Up(ctx, UpRequest{Spec: &spec.DevContainer{}, LocalWorkspace: "/x"}); err == nil {
		t.Error("expected error for missing Image")
	}
}

// Up's wait-for-Ready needs a real-ish Pod status which the fake
// clientset doesn't synthesize. The test sets ReadyTimeout to a
// tiny duration so wait fails immediately; we then assert what the
// Create call carried (labels, annotations, container).
func TestUp_CreatesPodWithDiscoveryLabel(t *testing.T) {
	cs := fake.NewSimpleClientset()
	h := New(cs, nil, Options{ReadyTimeout: 1})

	_, err := h.Up(context.Background(), UpRequest{
		Spec:           &spec.DevContainer{Image: "alpine:3"},
		LocalWorkspace: "/host/proj",
		Image:          "alpine:3",
	})
	// Wait will fail (the fake never reports Ready); that's fine.
	if err == nil || !strings.Contains(err.Error(), "wait for container") {
		t.Errorf("expected wait-for-Ready error, got %v", err)
	}

	var createdPod *corev1.Pod
	for _, action := range cs.Actions() {
		if ca, ok := action.(k8stesting.CreateAction); ok && ca.GetResource().Resource == "pods" {
			createdPod = ca.GetObject().(*corev1.Pod)
			break
		}
	}
	if createdPod == nil {
		t.Fatal("no Create call observed")
	}
	wantHash := LocalFolderHash("/host/proj")
	if got := createdPod.Labels[LabelLocalFolderHash]; got != wantHash {
		t.Errorf("LabelLocalFolderHash = %q, want %q", got, wantHash)
	}
	if got := createdPod.Annotations[AnnotationLocalFolder]; got != "/host/proj" {
		t.Errorf("AnnotationLocalFolder = %q", got)
	}
	if !strings.HasPrefix(createdPod.Name, "devcontainer-") {
		t.Errorf("Pod.Name = %q, want devcontainer-<hash>", createdPod.Name)
	}
	if len(createdPod.Spec.Containers) != 1 || createdPod.Spec.Containers[0].Image != "alpine:3" {
		t.Errorf("container = %+v", createdPod.Spec.Containers)
	}
}

// When a pod already matches the discovery label, Up reuses it and
// does not call Create at all.
func TestUp_ReusesExistingPod(t *testing.T) {
	hash := LocalFolderHash("/host/proj")
	existing := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "devcontainer-" + hash,
			Labels:    map[string]string{LabelLocalFolderHash: hash},
			UID:       "existing-uid",
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "devcontainer"}}},
	}
	cs := fake.NewSimpleClientset(existing)
	h := New(cs, nil, Options{ReadyTimeout: 1})

	_, _ = h.Up(context.Background(), UpRequest{
		Spec:           &spec.DevContainer{Image: "alpine:3"},
		LocalWorkspace: "/host/proj",
		Image:          "alpine:3",
	})
	for _, action := range cs.Actions() {
		if action.GetVerb() == "create" {
			t.Errorf("Create called on a reuse path: %+v", action)
		}
	}
}

// AlreadyExists at Create time means another caller raced us — Up
// must Get the existing pod and continue, not propagate the error.
func TestUp_AlreadyExistsRecoversByGet(t *testing.T) {
	hash := LocalFolderHash("/host/proj")
	existing := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "devcontainer-" + hash,
			Labels:    map[string]string{LabelLocalFolderHash: hash},
			UID:       "raced-uid",
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "devcontainer"}}},
	}
	cs := fake.NewSimpleClientset(existing)

	getCalled := false
	cs.PrependReactor("list", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		// First List returns empty so Up tries Create.
		return true, &corev1.PodList{}, nil
	})
	cs.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		gvr := schema.GroupResource{Group: "", Resource: "pods"}
		return true, nil, apierrors.NewAlreadyExists(gvr, existing.Name)
	})
	cs.PrependReactor("get", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		getCalled = true
		return true, existing.DeepCopy(), nil
	})

	h := New(cs, nil, Options{ReadyTimeout: 1})

	_, err := h.Up(context.Background(), UpRequest{
		Spec:           &spec.DevContainer{Image: "alpine:3"},
		LocalWorkspace: "/host/proj",
		Image:          "alpine:3",
	})
	// Wait failure is expected; "AlreadyExists" must NOT be in the
	// error chain.
	if err != nil && strings.Contains(err.Error(), "AlreadyExists") {
		t.Errorf("AlreadyExists leaked from Up: %v", err)
	}
	if !getCalled {
		t.Error("Up should Get the existing pod after AlreadyExists, but Get was not called")
	}
}
