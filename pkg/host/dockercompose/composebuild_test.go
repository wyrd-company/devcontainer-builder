package dockercompose

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func TestSelectBuildableServices_FiltersAndSkips(t *testing.T) {
	val := "v"
	_ = val
	proj := &types.Project{
		Name: "proj_abc",
		Services: types.Services{
			"app":     types.ServiceConfig{Name: "app", Image: "alpine"},
			"sidecar": types.ServiceConfig{Name: "sidecar", Image: "busybox", Build: &types.BuildConfig{Context: "."}},
			"helper":  types.ServiceConfig{Name: "helper", Build: &types.BuildConfig{Context: "./helper"}},
		},
	}
	got, err := selectBuildableServices(proj, nil, nil)
	if err != nil {
		t.Fatalf("selectBuildableServices: %v", err)
	}
	want := []string{"helper", "sidecar"}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("default selection: got %v want %v", got, want)
	}

	got, err = selectBuildableServices(proj, nil, map[string]bool{"sidecar": true})
	if err != nil {
		t.Fatalf("with skip: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"helper"}) {
		t.Errorf("skip=sidecar: got %v want [helper]", got)
	}

	got, err = selectBuildableServices(proj, []string{"helper"}, nil)
	if err != nil {
		t.Fatalf("with allow: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"helper"}) {
		t.Errorf("allow=[helper]: got %v want [helper]", got)
	}

	if _, err := selectBuildableServices(proj, []string{"nope"}, nil); err == nil {
		t.Error("expected error for unknown service in allow list")
	}
}

func TestResolveBuildTag(t *testing.T) {
	cases := []struct {
		name      string
		svc       types.ServiceConfig
		wantTag   string
		wantExtra []string
	}{
		{
			name:    "image only",
			svc:     types.ServiceConfig{Name: "s", Image: "registry/img:1"},
			wantTag: "registry/img:1",
		},
		{
			name: "build.tags wins, image folded into extras",
			svc: types.ServiceConfig{
				Name:  "s",
				Image: "registry/img:1",
				Build: &types.BuildConfig{Tags: types.StringList{"main:tag", "alt:tag"}},
			},
			wantTag:   "main:tag",
			wantExtra: []string{"alt:tag", "registry/img:1"},
		},
		{
			name:    "neither: generated tag prefixed",
			svc:     types.ServiceConfig{Name: "helper", Build: &types.BuildConfig{}},
			wantTag: "devcontainer-builder/compose-svc-proj_abc-helper:",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tag, extra := resolveBuildTag("proj_abc", tc.svc)
			if tc.name == "neither: generated tag prefixed" {
				if !strings.HasPrefix(tag, tc.wantTag) {
					t.Errorf("tag prefix: got %q want prefix %q", tag, tc.wantTag)
				}
				return
			}
			if tag != tc.wantTag {
				t.Errorf("tag: got %q want %q", tag, tc.wantTag)
			}
			if !reflect.DeepEqual(extra, tc.wantExtra) {
				t.Errorf("extras: got %v want %v", extra, tc.wantExtra)
			}
		})
	}
}

func TestBuildxFromService_FieldsThreaded(t *testing.T) {
	v := "1"
	proj := &types.Project{Name: "proj_abc", WorkingDir: "/work"}
	svc := types.ServiceConfig{
		Name:  "helper",
		Image: "registry/helper:1",
		Build: &types.BuildConfig{
			Context:    "./svc",
			Dockerfile: "Dockerfile.helper",
			Args:       types.MappingWithEquals{"K": &v},
			Labels:     types.Labels{"role": "helper"},
			CacheFrom:  types.StringList{"type=registry,ref=cache"},
			CacheTo:    types.StringList{"type=inline"},
			Target:     "build",
			Platforms:  types.StringList{"linux/amd64"},
			NoCache:    true,
			Pull:       true,
		},
	}
	in := BuildServiceImagesInputs{Builder: "mybuilder", Progress: "plain"}
	tag, extras := resolveBuildTag(proj.Name, svc)
	build, err := buildxFromService(proj, svc, tag, extras, in)
	if err != nil {
		t.Fatalf("buildxFromService: %v", err)
	}
	if build.ContextDir != "/work/svc" {
		t.Errorf("ContextDir: got %q want /work/svc", build.ContextDir)
	}
	if build.Dockerfile != "/work/svc/Dockerfile.helper" {
		t.Errorf("Dockerfile: got %q want /work/svc/Dockerfile.helper", build.Dockerfile)
	}
	if build.BuildArgs["K"] != "1" {
		t.Errorf("BuildArgs[K]: got %q want 1", build.BuildArgs["K"])
	}
	if build.Labels["role"] != "helper" {
		t.Errorf("Labels[role]: got %q want helper", build.Labels["role"])
	}
	if !reflect.DeepEqual(build.CacheFrom, []string{"type=registry,ref=cache"}) {
		t.Errorf("CacheFrom: got %v", build.CacheFrom)
	}
	if !reflect.DeepEqual(build.CacheTo, []string{"type=inline"}) {
		t.Errorf("CacheTo: got %v", build.CacheTo)
	}
	if build.Target != "build" {
		t.Errorf("Target: got %q want build", build.Target)
	}
	if !reflect.DeepEqual(build.Platforms, []string{"linux/amd64"}) {
		t.Errorf("Platforms: got %v want [linux/amd64]", build.Platforms)
	}
	if !build.NoCache || !build.Pull || !build.Load {
		t.Errorf("flags: got NoCache=%v Pull=%v Load=%v", build.NoCache, build.Pull, build.Load)
	}
	if build.Builder != "mybuilder" || build.Progress != "plain" {
		t.Errorf("builder/progress: got %q %q", build.Builder, build.Progress)
	}
	if !reflect.DeepEqual(build.Tags, []string{"registry/helper:1"}) {
		t.Errorf("Tags: got %v want [registry/helper:1]", build.Tags)
	}
}

func TestBuildxFromService_RejectsDockerfileInline(t *testing.T) {
	proj := &types.Project{Name: "proj", WorkingDir: "/w"}
	svc := types.ServiceConfig{
		Name:  "x",
		Build: &types.BuildConfig{DockerfileInline: "FROM alpine"},
	}
	_, err := buildxFromService(proj, svc, "x:1", nil, BuildServiceImagesInputs{})
	if err == nil {
		t.Fatal("expected error for dockerfile_inline")
	}
}

func TestApplyBuiltImages(t *testing.T) {
	proj := &types.Project{
		Services: types.Services{
			"app":     types.ServiceConfig{Name: "app", Image: ""},
			"sidecar": types.ServiceConfig{Name: "sidecar", Image: "stays"},
		},
	}
	applyBuiltImages(proj, map[string]string{"app": "built:1"})
	if proj.Services["app"].Image != "built:1" {
		t.Errorf("app image: got %q want built:1", proj.Services["app"].Image)
	}
	if proj.Services["sidecar"].Image != "stays" {
		t.Errorf("sidecar image clobbered: got %q want stays", proj.Services["sidecar"].Image)
	}
}
