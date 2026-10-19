// relationships:
//
//	references: [metadata, up, build, spawn]
package dockercompose

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

func TestPrimaryBuildSpecPersistsCompleteConfig(t *testing.T) {
	var dc spec.DevContainer
	if err := json.Unmarshal([]byte(`{"dockerComposeFile":"compose.yml","service":"app","features":{"./sample":{}},"overrideFeatureInstallOrder":["./sample"],"init":true,"privileged":true,"capAdd":["SYS_PTRACE"],"securityOpt":["no-new-privileges"],"mounts":["type=volume,target=/cache"],"containerUser":"root","remoteUser":"sample","containerEnv":{"A":"a"},"remoteEnv":{"B":"b"},"onCreateCommand":"create","updateContentCommand":["echo","update"],"postCreateCommand":{"sample":"post"},"postStartCommand":"start","postAttachCommand":"attach","overrideCommand":false,"customizations":{"editor":{"sample":true}}}`), &dc); err != nil {
		t.Fatal(err)
	}
	dc.Path = "/workspace/devcontainer.json"
	synthetic, err := primaryBuildSpec(&dc, "alpine:3.20")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(metadata.FromConfig(synthetic), metadata.FromConfig(&dc)) {
		t.Fatalf("config metadata lost: %+v", synthetic)
	}
	if synthetic.Image != "alpine:3.20" || synthetic.Mode() != spec.ModeSingle || synthetic.Path != dc.Path || synthetic.Features.Len() != 1 || !reflect.DeepEqual(synthetic.OverrideFeature, dc.OverrideFeature) {
		t.Fatalf("synthetic spec=%+v", synthetic)
	}
	var roundTrip spec.DevContainer
	if err := json.Unmarshal(synthetic.Raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(metadata.FromConfig(&roundTrip), metadata.FromConfig(&dc)) {
		t.Fatal("synthetic Raw loses metadata")
	}
}

func TestComposeImageMetadataProvenance(t *testing.T) {
	dc := &spec.DevContainer{PostCreate: spec.Command{Shell: "config"}, RemoteUser: "current"}
	resolved := []features.Resolved{{Source: "./sample", Metadata: features.Metadata{PostCreateCommand: spec.Command{Shell: "feature"}}}}
	complete, _ := metadata.Encode(metadata.Collect([]metadata.Entry{{PostCreate: spec.Command{Shell: "base"}}}, resolved, dc))
	for _, tc := range []struct {
		name  string
		built bool
		label string
		want  []string
	}{
		{"derived", true, complete, []string{"base", "feature", "config"}},
		{"prebuilt", false, complete, []string{"base", "feature", "config", "feature", "config"}},
		{"missing-label", true, "", []string{"feature", "config"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := imageMetadataEntries(UpRequest{Spec: dc, Features: resolved, ImageBuilt: tc.built}, map[string]string{metadata.Label: tc.label}, nil)
			var commands []string
			for _, phase := range metadata.Plan(entries) {
				commands = append(commands, phase.Command.Shell)
			}
			if !reflect.DeepEqual(commands, tc.want) {
				t.Fatalf("hooks=%v", commands)
			}
			if got, _ := metadata.Encode(entries); tc.name == "derived" && got != complete {
				t.Fatalf("derived container metadata=%s, want the image label %s", got, complete)
			}
		})
	}
}

func TestComposeMetadataRuntimeMappings(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "metadata", true: "native-or-union"}[native], func(t *testing.T) {
			var entries []metadata.Entry
			label := `[{"init":true,"privileged":true,"capAdd":["SYS_PTRACE"],"securityOpt":["no-new-privileges"],"containerUser":"${localEnv:USER}","remoteUser":"root","containerEnv":{"FROM_META":"${localEnv:VALUE}","PRIORITY":"metadata"},"remoteEnv":{"REMOTE":"remote"},"entrypoint":"echo ${localEnv:VALUE}","mounts":[{"type":"volume","source":"sample-cache","target":"/cache","readOnly":true},"type=bind,src=${localWorkspaceFolder},dst=/bind,consistency=cached,ro"]}]`
			entries, _ = metadata.Parse(label)
			if native {
				falsity := false
				entries[0].Init = &falsity
				entries[0].Privileged = &falsity
			}
			svc := types.ServiceConfig{Name: "app", Image: "alpine", User: "native", Command: types.ShellCommand{"serve"}, Environment: types.MappingWithEquals{"NATIVE": stringPtr("native"), "PRIORITY": stringPtr("native")}, Volumes: []types.ServiceVolumeConfig{{Type: "volume", Source: "native", Target: "/cache"}}}
			if native {
				truth := true
				svc.Init = &truth
				svc.Privileged = true
				svc.CapAdd = []string{"NET_ADMIN", "SYS_PTRACE"}
				svc.SecurityOpt = []string{"seccomp=unconfined", "no-new-privileges"}
			}
			proj := &types.Project{Services: types.Services{"app": svc}, Volumes: types.Volumes{"sample-cache": {Name: "native-scoped"}}}
			sub := substitute.Context{LocalWorkspaceFolder: "/workspace", LocalEnv: map[string]string{"USER": "sample", "VALUE": "hello $LITERAL"}}
			merged, err := runtimeMetadata(&spec.DevContainer{Service: "app"}, entries, sub)
			if err != nil {
				t.Fatal(err)
			}
			applyPrimaryMetadata(proj, merged.DevContainer, UpRequest{}, sub)
			got := proj.Services["app"]
			translated, err := translateService(proj, got, "sample", nil, nil, map[string]string{"sample-cache": "native-scoped"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			host, cfg := translated.HostConfig, translated.Config
			if host.Init == nil || !*host.Init || !host.Privileged || !containsAll([]string(host.CapAdd), "SYS_PTRACE") || !containsAll(host.SecurityOpt, "no-new-privileges") {
				t.Fatalf("host=%+v", host)
			}
			if native && (!containsAll([]string(host.CapAdd), "NET_ADMIN") || !containsAll(host.SecurityOpt, "seccomp=unconfined") || len(host.CapAdd) != 2 || len(host.SecurityOpt) != 2) {
				t.Fatalf("native settings lost: %+v", host)
			}
			if cfg.User != "sample" || merged.RemoteUser != "root" || !reflect.DeepEqual(got.Command, svc.Command) || !containsAll(cfg.Env, "NATIVE=native", "PRIORITY=metadata", "FROM_META=hello $LITERAL") || containsAll(cfg.Env, "REMOTE=remote") || !reflect.DeepEqual(merged.Entrypoints, []string{"echo hello $LITERAL"}) {
				t.Fatalf("config=%+v merged=%+v", cfg, merged)
			}
			// As in docker compose, plain binds and declared volumes use the
			// Bind API, which has no consistency option.
			if len(host.Mounts) != 0 || !reflect.DeepEqual(host.Binds, []string{"native-scoped:/cache:ro", "/workspace:/bind:ro"}) {
				t.Fatalf("binds=%v mounts=%+v", host.Binds, host.Mounts)
			}
			if len(proj.Volumes) != 1 || proj.Volumes["sample-cache"].Name != "native-scoped" {
				t.Fatalf("volumes=%+v", proj.Volumes)
			}
			encoded, _ := metadata.Encode(entries)
			if !strings.Contains(encoded, "${localEnv:VALUE}") {
				t.Fatal("mutated raw metadata")
			}
		})
	}
}

func TestComposeAbsentMetadataPreservesNativeScalars(t *testing.T) {
	svc := types.ServiceConfig{Name: "app", User: "native", Environment: types.MappingWithEquals{"NATIVE": stringPtr("value")}, Command: types.ShellCommand{"serve"}}
	proj := &types.Project{Services: types.Services{"app": svc}}
	merged, _ := runtimeMetadata(&spec.DevContainer{Service: "app"}, nil, substitute.Context{})
	applyPrimaryMetadata(proj, merged.DevContainer, UpRequest{}, substitute.Context{})
	if got := proj.Services["app"]; got.User != svc.User || !reflect.DeepEqual(got.Environment, svc.Environment) || !reflect.DeepEqual(got.Command, svc.Command) {
		t.Fatalf("native settings=%+v", got)
	}
}

func TestComposeFeatureEnvironmentPrecedence(t *testing.T) {
	resolved := []features.Resolved{{Metadata: features.Metadata{ContainerEnv: map[string]string{"A": "one", "B": "${localEnv:VALUE}", "C": "feature"}}}, {Metadata: features.Metadata{ContainerEnv: map[string]string{"A": "two"}}}}
	for _, built := range []bool{false, true} {
		t.Run(map[bool]string{false: "unbuilt", true: "built"}[built], func(t *testing.T) {
			proj := &types.Project{Services: types.Services{"app": {Environment: types.MappingWithEquals{"NATIVE": stringPtr("native")}}}}
			applyPrimaryMetadata(proj, &spec.DevContainer{Service: "app", ContainerEnv: map[string]any{"C": "config"}}, UpRequest{Features: resolved, ImageBuilt: built}, substitute.Context{LocalEnv: map[string]string{"VALUE": "hello"}})
			env := envToStringMap(proj.Services["app"].Environment)
			if env["C"] != "config" || env["NATIVE"] != "native" {
				t.Fatalf("env=%v", env)
			}
			if built {
				if _, ok := env["A"]; ok {
					t.Fatal("built features overlaid image env")
				}
			} else if env["A"] != "two" || env["B"] != "hello" {
				t.Fatalf("env=%v", env)
			}
		})
	}
}

func TestComposeRuntimePlanSkipsRetainRawHooks(t *testing.T) {
	entries := []metadata.Entry{{ID: "./sample", PostCreate: spec.Command{Parallel: map[string]spec.Command{"shell": {Shell: "echo ${localEnv:VALUE} ${containerEnv:VALUE}"}, "argv": {Args: []string{"echo", "${localEnv:VALUE} ${containerEnv:VALUE}"}}}}, PostAttach: spec.Command{Shell: "attach"}}}
	before, _ := metadata.Encode(entries)
	for _, tc := range []struct {
		name string
		req  UpRequest
		want int
	}{{"all", UpRequest{}, 2}, {"skip-all", UpRequest{SkipLifecycle: true}, 0}, {"skip-attach", UpRequest{SkipPostAttach: true}, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			plan := runtimePlan(tc.req, entries, substitute.Context{LocalEnv: map[string]string{"VALUE": "local"}}, map[string]string{"VALUE": "container"})
			if len(plan) != tc.want {
				t.Fatalf("plan=%+v", plan)
			}
			if len(plan) > 0 && (plan[0].Command.Parallel["shell"].Shell != "echo local container" || plan[0].Command.Parallel["argv"].Args[1] != "local container" || plan[0].Origin.Source != "./sample") {
				t.Fatalf("plan=%+v", plan)
			}
			after, _ := metadata.Encode(entries)
			if before != after {
				t.Fatal("skip/substitution mutated persisted hooks")
			}
		})
	}
}

func TestMetadataVolumeForms(t *testing.T) {
	for _, tc := range []struct {
		name     string
		input    any
		want     types.ServiceVolumeConfig
		external bool
	}{
		{"aliases", "src=/source,destination=/target,ro=1,consistency=delegated", types.ServiceVolumeConfig{Type: "bind", Source: "/source", Target: "/target", ReadOnly: true, Consistency: "delegated"}, false},
		{"false", map[string]any{"source": "/source", "target": "/target", "readonly": false, "external": false}, types.ServiceVolumeConfig{Type: "bind", Source: "/source", Target: "/target"}, false},
		{"string-bool", map[string]any{"type": "volume", "source": "sample", "target": "/target", "readonly": "true"}, types.ServiceVolumeConfig{Type: "volume", Source: "sample", Target: "/target", ReadOnly: true}, false},
		{"external-string", "type=volume,source=sample,target=/target,external=true", types.ServiceVolumeConfig{Type: "volume", Source: "sample", Target: "/target"}, true},
		{"external-object", map[string]any{"type": "volume", "source": "sample", "target": "/target", "external": true}, types.ServiceVolumeConfig{Type: "volume", Source: "sample", Target: "/target"}, true},
		{"invalid", 42, types.ServiceVolumeConfig{Type: "bind"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, external := metadataVolume(tc.input); !reflect.DeepEqual(got, tc.want) || external != tc.external {
				t.Fatalf("mount=%+v external=%v want=%+v %v", got, external, tc.want, tc.external)
			}
		})
	}
}
func containsAll(values []string, want ...string) bool {
	for _, value := range want {
		found := false
		for _, got := range values {
			if value == got {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func stringPtr(s string) *string { return &s }

func TestComposeMetadataLabelAffectsConfigHash(t *testing.T) {
	native := types.Labels{"sample.label": "native"}
	proj := &types.Project{Services: types.Services{"app": {Image: "alpine", Labels: native}}}
	before := serviceConfigHash(proj.Services["app"])
	entries := []metadata.Entry{{PostCreate: spec.Command{Shell: "first"}}}
	if err := setPrimaryMetadata(proj, "app", entries); err != nil {
		t.Fatal(err)
	}
	first := serviceConfigHash(proj.Services["app"])
	if first == before {
		t.Fatal("metadata producer did not affect service hash")
	}
	entries[0].PostCreate.Shell = "second"
	if err := setPrimaryMetadata(proj, "app", entries); err != nil {
		t.Fatal(err)
	}
	if first == serviceConfigHash(proj.Services["app"]) {
		t.Fatal("hook-only change did not affect service hash")
	}
	value, _ := metadata.Encode(entries)
	if proj.Services["app"].Labels[metadata.Label] != value || proj.Services["app"].Labels["sample.label"] != "native" || len(native) != 1 {
		t.Fatalf("labels=%v original=%v", proj.Services["app"].Labels, native)
	}
}

func TestComposeMetadataVolumeDeclarations(t *testing.T) {
	proj := &types.Project{Name: "sample-project", Services: types.Services{"app": {Image: "alpine", Volumes: []types.ServiceVolumeConfig{{Type: "volume", Source: "native", Target: "/native"}}}}, Volumes: types.Volumes{"native": {Name: "native-scoped"}}}
	dc := &spec.DevContainer{Service: "app", Mounts: []any{
		"type=volume,source=native,target=/reuse",
		"type=volume,source=scoped,target=/scoped",
		map[string]any{"type": "volume", "source": "shared", "target": "/shared", "external": true},
		"type=bind,source=/missing-target",
		map[string]any{"type": "volume", "source": "missing-target"},
	}}
	applyPrimaryMetadata(proj, dc, UpRequest{}, substitute.Context{})
	var sources []string
	for _, vol := range proj.Services["app"].Volumes {
		sources = append(sources, vol.Source)
	}
	if !reflect.DeepEqual(sources, []string{"native", "native", "scoped", "shared"}) {
		t.Fatalf("service volume sources=%v", sources)
	}
	want := types.Volumes{
		"native": {Name: "native-scoped"},
		"scoped": {Name: "sample-project_scoped"},
		"shared": {Name: "shared", External: true},
	}
	if !reflect.DeepEqual(proj.Volumes, want) {
		t.Fatalf("declarations=%+v", proj.Volumes)
	}
}

// Request mounts follow the metadata mounts and declare their named volumes.
func TestComposeRequestMounts(t *testing.T) {
	proj := &types.Project{Name: "sample-project", WorkingDir: "/project", Services: types.Services{"app": {Image: "alpine"}}}
	dc := &spec.DevContainer{Service: "app", Mounts: []any{"type=bind,source=/host/data,target=/data"}}
	req := UpRequest{Mounts: []string{
		"type=volume,source=cache,target=/cache",
		"type=volume,source=shared,target=/shared,external=true",
		"type=bind,source=/host/extra,target=/data",
		"type=bind,source=relative/dir,target=/relative",
	}}
	applyPrimaryMetadata(proj, dc, req, substitute.Context{})
	got := proj.Services["app"].Volumes
	want := []types.ServiceVolumeConfig{
		{Type: "volume", Source: "cache", Target: "/cache"},
		{Type: "volume", Source: "shared", Target: "/shared"},
		{Type: "bind", Source: "/host/extra", Target: "/data"},
		{Type: "bind", Source: "/project/relative/dir", Target: "/relative"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("volumes=%+v", got)
	}
	declared := types.Volumes{"cache": {Name: "sample-project_cache"}, "shared": {Name: "shared", External: true}}
	if !reflect.DeepEqual(proj.Volumes, declared) {
		t.Fatalf("declarations=%+v", proj.Volumes)
	}
	// Without request mounts the service keeps only its configured mounts.
	plain := &types.Project{Name: "sample-project", Services: types.Services{"app": {Image: "alpine"}}}
	applyPrimaryMetadata(plain, dc, UpRequest{}, substitute.Context{})
	if v := plain.Services["app"].Volumes; len(v) != 1 || v[0].Source != "/host/data" || plain.Volumes != nil {
		t.Fatalf("plain volumes=%+v declarations=%+v", v, plain.Volumes)
	}
}
