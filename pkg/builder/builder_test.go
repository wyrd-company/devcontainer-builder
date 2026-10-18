package builder

import (
	"reflect"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestValidateBuildSupport_RejectsBuildOptions(t *testing.T) {
	dc := &spec.DevContainer{Build: &spec.Build{Options: []string{"--ssh", "default"}}}
	err := validateBuildSupport(dc)
	if err == nil || !strings.Contains(err.Error(), "build.options is not supported") {
		t.Fatalf("err = %v, want build.options rejection", err)
	}
	if err := validateBuildSupport(&spec.DevContainer{Build: &spec.Build{}}); err != nil {
		t.Fatalf("spec without build.options rejected: %v", err)
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
