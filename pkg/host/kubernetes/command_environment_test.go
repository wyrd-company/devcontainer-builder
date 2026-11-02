// relationships:
//
//	implements: schema-conformance
package kubernetes

import (
	oci "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"strings"
	"testing"
)

func TestBuildPodImageConfig(t *testing.T) {
	h := New(nil, nil, Options{})
	v := false
	dc := &spec.DevContainer{OverrideCommand: &v}
	req := UpRequest{Image: "alpine", ImageConfig: &oci.ImageConfig{Entrypoint: []string{"/entry"}, Cmd: []string{"argument"}}}
	p, err := h.buildPod(dc, req, "default", "sample", "hash", "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	args := p.Spec.Containers[0].Args
	if len(args) != 5 || args[3] != "/entry" || args[4] != "argument" {
		t.Fatalf("args=%q", args)
	}
	req.ImageConfig = nil
	if _, err := h.buildPod(dc, req, "default", "sample", "hash", "/workspace"); err == nil || !strings.Contains(err.Error(), "kubernetes host: project: kubernetes projection: ImageConfig") {
		t.Fatalf("err=%v", err)
	}
}
