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
			translated, err := translateService(proj, got, "sample", nil, nil, map[string]string{"dcb-metadata-sample-cache": "sample-cache"}, nil)
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
			if cfg.User != "sample" || merged.RemoteUser != "root" || !reflect.DeepEqual(got.Command, svc.Command) || !containsAll(cfg.Env, "NATIVE=native", "PRIORITY=metadata", "FROM_META=hello $LITERAL", "REMOTE=remote") || !reflect.DeepEqual(merged.Entrypoints, []string{"echo hello $LITERAL"}) {
				t.Fatalf("config=%+v merged=%+v", cfg, merged)
			}
			if len(host.Mounts) != 2 || host.Mounts[0].Source != "sample-cache" || host.Mounts[0].Target != "/cache" || !host.Mounts[0].ReadOnly || host.Mounts[1].Source != "/workspace" || host.Mounts[1].Target != "/bind" || !host.Mounts[1].ReadOnly || host.Mounts[1].Consistency != "cached" {
				t.Fatalf("mounts=%+v", host.Mounts)
			}
			if proj.Volumes["sample-cache"].Name != "native-scoped" || !proj.Volumes["dcb-metadata-sample-cache"].External {
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
		name  string
		input any
		want  types.ServiceVolumeConfig
	}{
		{"aliases", "src=/source,destination=/target,ro=1,consistency=delegated", types.ServiceVolumeConfig{Type: "bind", Source: "/source", Target: "/target", ReadOnly: true, Consistency: "delegated"}},
		{"false", map[string]any{"source": "/source", "target": "/target", "readonly": false}, types.ServiceVolumeConfig{Type: "bind", Source: "/source", Target: "/target"}},
		{"string-bool", map[string]any{"type": "volume", "source": "sample", "target": "/target", "readonly": "true"}, types.ServiceVolumeConfig{Type: "volume", Source: "sample", Target: "/target", ReadOnly: true}},
		{"invalid", 42, types.ServiceVolumeConfig{Type: "bind"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := metadataVolume(tc.input); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("mount=%+v want=%+v", got, tc.want)
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

func TestComposeMetadataVolumeCollisionAndInvalidTargets(t *testing.T) {
	proj := &types.Project{Services: types.Services{"app": {Image: "alpine", Volumes: []types.ServiceVolumeConfig{{Type: "volume", Source: "sample", Target: "/native"}}}}, Volumes: types.Volumes{"sample": {Name: "native-scoped"}, "dcb-metadata-sample": {Name: "other-native"}}}
	dc := &spec.DevContainer{Service: "app", Mounts: []any{"type=volume,source=sample,target=/cache", "type=bind,source=/missing-target", map[string]any{"type": "volume", "source": "missing-target"}}}
	applyPrimaryMetadata(proj, dc, UpRequest{}, substitute.Context{})
	svc := proj.Services["app"]
	if len(svc.Volumes) != 2 || svc.Volumes[0].Source != "sample" || svc.Volumes[1].Source != "dcb-metadata-sample-" {
		t.Fatalf("volumes=%+v", svc.Volumes)
	}
	if proj.Volumes["sample"].Name != "native-scoped" || proj.Volumes["dcb-metadata-sample"].Name != "other-native" || proj.Volumes["dcb-metadata-sample-"].Name != "sample" || !proj.Volumes["dcb-metadata-sample-"].External {
		t.Fatalf("declarations=%+v", proj.Volumes)
	}
}
