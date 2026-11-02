// relationships:
//
//	references: [dockerfile, gateway]
package buildkit

import (
	"context"
	"strconv"
	"testing"
)

func TestDockerfileResourceFrontend(t *testing.T) {
	for _, size := range []int64{0, 134217728} {
		t.Run(strconv.FormatInt(size, 10), func(t *testing.T) {
			spec := buildSpecWithUpstream()
			spec.Upstream.ShmSize = size
			spec.Upstream.Ulimits = "core=0:0,nofile=1024:2048"
			gc := &fakeGateway{baseConfig: baseConfigJSON(t)}
			if _, _, err := buildOnePlatform(context.Background(), gc, spec, amd64, buildSettings{}); err != nil {
				t.Fatal(err)
			}
			if len(gc.frontends) != 1 {
				t.Fatalf("frontends=%v", gc.frontends)
			}
			attrs := gc.frontends[0].FrontendOpt
			if attrs["ulimit"] != "core=0:0,nofile=1024:2048" {
				t.Fatalf("ulimit=%q", attrs["ulimit"])
			}
			got, present := attrs["shm-size"]
			if present != (size > 0) || (size > 0 && got != "134217728") {
				t.Fatalf("shm-size=%q present=%v", got, present)
			}
		})
	}
	if attrs := dockerfileFrontendAttrs(&DockerfileBuild{}, buildSettings{}); attrs["ulimit"] != "" {
		t.Fatalf("default ulimit=%v", attrs)
	}
}
