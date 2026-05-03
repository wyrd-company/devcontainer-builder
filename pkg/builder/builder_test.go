package builder

import (
	"reflect"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestValidateBuildSupport_RejectsBuildKitOptions(t *testing.T) {
	dc := &spec.DevContainer{Build: &spec.Build{Options: []string{"--ssh", "default"}}}
	if err := validateBuildSupport(dc, EngineBuildKit); err == nil {
		t.Fatal("expected buildkit build.options rejection")
	}
	if err := validateBuildSupport(dc, EngineBuildx); err != nil {
		t.Fatalf("buildx should accept build.options: %v", err)
	}
}

func TestBuildCacheFrom_MergesSpecAndCLI(t *testing.T) {
	dc := &spec.DevContainer{Build: &spec.Build{CacheFrom: spec.StringOrSlice{"type=registry,ref=spec-cache"}}}
	got := mergeSlices(buildCacheFrom(dc), []string{"type=local,src=.cache"})
	want := []string{"type=registry,ref=spec-cache", "type=local,src=.cache"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cacheFrom merge = %v, want %v", got, want)
	}
}
