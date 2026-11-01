package dockercompose

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/wyrd-company/devcontainer-builder/pkg/buildkit"
)

func TestSelectBuildableServices_FiltersAndSkips(t *testing.T) {
	val := "v"
	_ = val
	proj := &types.Project{
		Name: "proj_abc",
		Services: types.Services{
			"app":     types.ServiceConfig{Name: "app", ContainerSpec: types.ContainerSpec{Image: "alpine"}},
			"sidecar": types.ServiceConfig{Name: "sidecar", ContainerSpec: types.ContainerSpec{Image: "busybox"}, WorkloadSpec: types.WorkloadSpec{Build: &types.BuildConfig{Context: "."}}},
			"helper":  types.ServiceConfig{Name: "helper", WorkloadSpec: types.WorkloadSpec{Build: &types.BuildConfig{Context: "./helper"}}},
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
			svc:     types.ServiceConfig{Name: "s", ContainerSpec: types.ContainerSpec{Image: "registry/img:1"}},
			wantTag: "registry/img:1",
		},
		{
			name: "build.tags wins, image folded into extras",
			svc: types.ServiceConfig{
				Name: "s",
				ContainerSpec: types.ContainerSpec{
					Image: "registry/img:1",
				},
				WorkloadSpec: types.WorkloadSpec{
					Build: &types.BuildConfig{Tags: types.StringList{"main:tag", "alt:tag"}},
				},
			},
			wantTag:   "main:tag",
			wantExtra: []string{"alt:tag", "registry/img:1"},
		},
		{
			name:    "neither: generated tag prefixed",
			svc:     types.ServiceConfig{Name: "helper", WorkloadSpec: types.WorkloadSpec{Build: &types.BuildConfig{}}},
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

func TestServiceBuild_FieldsThreaded(t *testing.T) {
	v := "1"
	proj := &types.Project{Name: "proj_abc", WorkingDir: "/work"}
	svc := types.ServiceConfig{
		Name: "helper",
		ContainerSpec: types.ContainerSpec{
			Image: "registry/helper:1",
		},
		WorkloadSpec: types.WorkloadSpec{
			Build: &types.BuildConfig{
				Context:    "./svc",
				Dockerfile: "docker/Dockerfile.helper",
				Args:       types.MappingWithEquals{"K": &v},
				Labels:     types.Labels{"role": "helper"},
				CacheFrom:  types.StringList{"type=registry,ref=cache"},
				CacheTo:    types.StringList{"type=inline"},
				Target:     "build",
				Platforms:  types.StringList{"linux/amd64"},
				NoCache:    true,
				Pull:       true,
			},
		},
	}
	in := BuildServiceImagesInputs{BuildKitAddress: "tcp://buildkit.test:1234", Progress: "plain"}
	tag, extras := resolveBuildTag(proj.Name, svc)
	build, opt, err := serviceBuild(proj, svc, tag, extras, in)
	if err != nil {
		t.Fatalf("serviceBuild: %v", err)
	}
	wantBuild := buildkit.DockerfileBuild{
		ContextDir:    "/work/svc",
		DockerfileDir: "/work/svc/docker",
		Filename:      "Dockerfile.helper",
		Target:        "build",
		BuildArgs:     map[string]string{"K": "1"},
		Labels:        map[string]string{"role": "helper"},
	}
	if !reflect.DeepEqual(build, wantBuild) {
		t.Errorf("build = %+v, want %+v", build, wantBuild)
	}
	if !reflect.DeepEqual(opt.CacheImports, []buildkit.CacheRef{{Type: "registry", Attrs: map[string]string{"ref": "cache"}}}) {
		t.Errorf("CacheImports: got %v", opt.CacheImports)
	}
	if !reflect.DeepEqual(opt.CacheExports, []buildkit.CacheRef{{Type: "inline", Attrs: map[string]string{}}}) {
		t.Errorf("CacheExports: got %v", opt.CacheExports)
	}
	if !reflect.DeepEqual(opt.Platforms, []string{"linux/amd64"}) {
		t.Errorf("Platforms: got %v want [linux/amd64]", opt.Platforms)
	}
	if !opt.NoCache || !opt.Pull || !opt.Load || opt.Push {
		t.Errorf("flags: got NoCache=%v Pull=%v Load=%v Push=%v", opt.NoCache, opt.Pull, opt.Load, opt.Push)
	}
	if opt.Address != "tcp://buildkit.test:1234" || opt.Progress != "plain" {
		t.Errorf("address/progress: got %q %q", opt.Address, opt.Progress)
	}
	if !reflect.DeepEqual(opt.Tags, []string{"registry/helper:1"}) {
		t.Errorf("Tags: got %v want [registry/helper:1]", opt.Tags)
	}
}

func TestServiceBuild_DefaultDockerfile(t *testing.T) {
	proj := &types.Project{Name: "proj", WorkingDir: "/w"}
	svc := types.ServiceConfig{Name: "x", WorkloadSpec: types.WorkloadSpec{Build: &types.BuildConfig{Context: "."}}}
	build, _, err := serviceBuild(proj, svc, "x:1", nil, BuildServiceImagesInputs{})
	if err != nil {
		t.Fatal(err)
	}
	if build.ContextDir != "/w" || build.DockerfileDir != "/w" || build.Filename != "Dockerfile" {
		t.Fatalf("build = %+v, want /w/Dockerfile", build)
	}
}

func TestServiceBuild_RejectsDockerfileInline(t *testing.T) {
	proj := &types.Project{Name: "proj", WorkingDir: "/w"}
	svc := types.ServiceConfig{
		Name: "x",
		WorkloadSpec: types.WorkloadSpec{
			Build: &types.BuildConfig{DockerfileInline: "FROM alpine"},
		},
	}
	_, _, err := serviceBuild(proj, svc, "x:1", nil, BuildServiceImagesInputs{})
	if err == nil {
		t.Fatal("expected error for dockerfile_inline")
	}
}

func TestApplyBuiltImages(t *testing.T) {
	proj := &types.Project{
		Services: types.Services{
			"app":     types.ServiceConfig{Name: "app", ContainerSpec: types.ContainerSpec{Image: ""}},
			"sidecar": types.ServiceConfig{Name: "sidecar", ContainerSpec: types.ContainerSpec{Image: "stays"}},
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
