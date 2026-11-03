// relationships:
//
//	references: [dockerfile, gateway]
package buildkit

import (
	"context"
	"testing"
)

func TestDockerfileCgroupFrontend(t *testing.T) {
	for _, parent := range []string{"", "/sample", "sample.slice", "sample:scope/value with spaces"} {
		t.Run(parent, func(t *testing.T) {
			spec := buildSpecWithUpstream()
			spec.Upstream.CgroupParent = parent
			direct := dockerfileFrontendAttrs(spec.Upstream, buildSettings{})
			gc := &fakeGateway{baseConfig: baseConfigJSON(t)}
			if _, _, err := buildOnePlatform(context.Background(), gc, spec, amd64, buildSettings{}); err != nil {
				t.Fatal(err)
			}
			if len(gc.frontends) != 1 {
				t.Fatalf("frontends=%v", gc.frontends)
			}
			for route, attrs := range map[string]map[string]string{"standalone": direct, "nested": gc.frontends[0].FrontendOpt} {
				got, present := attrs["cgroup-parent"]
				if present != (parent != "") || got != parent {
					t.Fatalf("%s parent=%q present=%v want=%q", route, got, present, parent)
				}
			}
		})
	}
}
