// relationships:
//
//	implements: schema-conformance
package kubernetes

import (
	oci "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"testing"
)

func TestBuildPodRunArgsEnvironment(t *testing.T) {
	h := &Host{}
	dc := &spec.DevContainer{RunArgs: []string{"-iteSAMPLE=value", "-eINHERITED"}}
	req := UpRequest{Image: "alpine", LocalEnv: map[string]string{"INHERITED": "caller"}, ImageConfig: &oci.ImageConfig{}}
	p, err := h.buildPod(dc, req, "default", "sample", "hash", "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	c := p.Spec.Containers[0]
	if !c.Stdin || !c.TTY {
		t.Fatal("stdin/tty lost")
	}
	got := map[string]string{}
	for _, e := range c.Env {
		got[e.Name] = e.Value
	}
	if got["SAMPLE"] != "value" || got["INHERITED"] != "caller" {
		t.Fatal(got)
	}
	dc.RunArgs = []string{"-eABSENT"}
	if _, err := h.buildPod(dc, req, "default", "sample", "hash", "/workspace"); err != nil {
		t.Fatal(err)
	}
}
