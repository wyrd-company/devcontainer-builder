package kubernetes

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestProject_ForwardPorts(t *testing.T) {
	dc := &spec.DevContainer{
		ForwardPorts: []any{
			float64(3000), // numeric (devcontainer.json parses ints as float64)
			"8080",        // string
			"5353/udp",    // with proto
		},
		AppPort: float64(80),
	}
	pj, err := Project(ProjectRequest{Spec: dc, Image: "x"})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	ports := pj.Pod.Spec.Containers[0].Ports
	if len(ports) != 4 {
		t.Fatalf("Ports len = %d, want 4", len(ports))
	}
	want := []corev1.ContainerPort{
		{ContainerPort: 3000, Protocol: corev1.ProtocolTCP},
		{ContainerPort: 8080, Protocol: corev1.ProtocolTCP},
		{ContainerPort: 5353, Protocol: corev1.ProtocolUDP},
		{ContainerPort: 80, Protocol: corev1.ProtocolTCP},
	}
	for i, w := range want {
		got := ports[i]
		if got.ContainerPort != w.ContainerPort || got.Protocol != w.Protocol {
			t.Errorf("port[%d] = %+v, want %+v", i, got, w)
		}
	}
}

func TestProject_ForwardPortsRange(t *testing.T) {
	dc := &spec.DevContainer{
		ForwardPorts: []any{"8000-8002"},
	}
	pj, err := Project(ProjectRequest{Spec: dc, Image: "x"})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	ports := pj.Pod.Spec.Containers[0].Ports
	if len(ports) != 3 {
		t.Fatalf("Ports len = %d, want 3 (range expansion)", len(ports))
	}
	for i, p := range ports {
		if p.ContainerPort != int32(8000+i) {
			t.Errorf("port[%d].ContainerPort = %d, want %d", i, p.ContainerPort, 8000+i)
		}
	}
}

func TestProject_ForwardPortsHostPortRejected(t *testing.T) {
	dc := &spec.DevContainer{
		ForwardPorts: []any{"8080:8080"},
	}
	_, err := Project(ProjectRequest{Spec: dc, Image: "x"})
	if err == nil {
		t.Fatal("expected error for host:container port form")
	}
}
