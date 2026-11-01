// relationships:
//
//	references: [metadata, spawn, up]
package docker

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestRuntimeMetadata(t *testing.T) {
	entries, err := metadata.Parse(`[{"id":"base","init":true,"privileged":true,"capAdd":["SYS_PTRACE"],"securityOpt":["no-new-privileges"],"entrypoint":"echo ${localEnv:GREETING}","mounts":[{"type":"volume","source":"sample-${localWorkspaceFolderBasename}","target":"/cache"}],"containerUser":"${localEnv:GREETING}","remoteUser":"${localEnv:GREETING}","containerEnv":{"A":"${localEnv:GREETING}"}},{"init":false,"privileged":false,"capAdd":["SYS_PTRACE","NET_ADMIN"]}]`)
	if err != nil {
		t.Fatal(err)
	}
	dc, entrypoints, err := runtimeMetadata(&spec.DevContainer{Image: "alpine"}, entries, substituteCtxForTest())
	if err != nil {
		t.Fatal(err)
	}
	cfg, host, _, err := buildContainerConfigWithEntrypoints(dc, nil, entrypoints, "alpine", "/workspace", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !host.Privileged || host.Init == nil || !*host.Init || !reflect.DeepEqual([]string(host.CapAdd), []string{"SYS_PTRACE", "NET_ADMIN"}) || !reflect.DeepEqual(host.SecurityOpt, []string{"no-new-privileges"}) {
		t.Fatalf("host=%+v", host)
	}
	if cfg.User != "hello" || dc.RemoteUser != "hello" || !strings.Contains(cfg.Cmd[1], "echo hello") || !reflect.DeepEqual(cfg.Env, []string{"A=hello"}) {
		t.Fatalf("config=%+v remoteUser=%q", cfg, dc.RemoteUser)
	}
	if len(host.Mounts) != 1 || host.Mounts[0].Source != "sample-proj" || host.Mounts[0].Target != "/cache" {
		t.Fatalf("mounts=%+v", host.Mounts)
	}
	if entries[0].RemoteUser != "${localEnv:GREETING}" {
		t.Fatal("mutated raw metadata")
	}
}

func TestCreatePersistsRawMetadataAndWrapsEntrypoints(t *testing.T) {
	h, created := fakeDaemon(t, `{"Entrypoint":["/image-init"],"Cmd":["serve"]}`)
	entries, err := metadata.Parse(`[{"id":"base","overrideCommand":false,"entrypoint":"echo ${localEnv:GREETING}","postCreateCommand":["echo","${localEnv:GREETING}"]}]`)
	if err != nil {
		t.Fatal(err)
	}
	dc, entrypoints, err := runtimeMetadata(&spec.DevContainer{Image: "alpine"}, entries, substituteCtxForTest())
	if err != nil {
		t.Fatal(err)
	}
	req := UpRequest{Image: "alpine", metadataEntries: entries, entrypoints: entrypoints}
	if _, err := h.create(context.Background(), dc, req, "/workspace"); err != nil {
		t.Fatal(err)
	}
	value, err := metadata.Encode(entries)
	if err != nil {
		t.Fatal(err)
	}
	if created.Labels[metadata.Label] != value || !strings.Contains(created.Cmd[1], "echo hello") || strings.Join(created.Cmd[2:], " ") != "- /image-init serve" {
		t.Fatalf("created=%+v", created)
	}
	persisted, err := metadata.Parse(created.Labels[metadata.Label])
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Plan(persisted)[0].Command.Args[1] != "${localEnv:GREETING}" {
		t.Fatal("raw lifecycle lost")
	}
}

func TestFeatureRuntimeRequirements(t *testing.T) {
	truth := true
	resolved := []features.Resolved{{Source: "./sample", Metadata: features.Metadata{Init: &truth, Privileged: &truth, CapAdd: []string{"SYS_PTRACE"}, SecurityOpt: []string{"no-new-privileges"}, Mounts: []any{"type=volume,target=/sample"}}}}
	_, host, _, err := buildContainerConfig(&spec.DevContainer{}, resolved, "alpine", "/workspace", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !host.Privileged || host.Init == nil || !*host.Init || len(host.CapAdd) != 1 || len(host.SecurityOpt) != 1 || len(host.Mounts) != 1 {
		t.Fatalf("feature requirements=%+v", host)
	}
}

func TestMetadataSubstitutionEscapesValues(t *testing.T) {
	entries := []metadata.Entry{{RemoteUser: "${localEnv:GREETING}", PostCreate: spec.Command{Parallel: map[string]spec.Command{"sample": {Args: []string{"echo", "${localEnv:GREETING}"}}}}}}
	value := "quote\" slash\\ newline\n"
	out, err := metadata.Substitute(entries, func(s string) string { return strings.ReplaceAll(s, "${localEnv:GREETING}", value) })
	if err != nil {
		t.Fatal(err)
	}
	if out[0].RemoteUser != value || out[0].PostCreate.Parallel["sample"].Args[1] != value {
		data, _ := json.Marshal(out)
		t.Fatalf("substitution=%s", data)
	}
}

func TestImageMetadataEntriesAvoidDuplicateBuiltHooks(t *testing.T) {
	truth := true
	dc := &spec.DevContainer{PostCreate: spec.Command{Shell: "echo config"}, RemoteUser: "sample"}
	resolved := []features.Resolved{{Source: "./sample", Metadata: features.Metadata{PostCreateCommand: spec.Command{Shell: "echo feature"}, Init: &truth}}}
	value, err := metadata.Encode(metadata.Collect(nil, resolved, dc))
	if err != nil {
		t.Fatal(err)
	}
	req := UpRequest{Spec: dc, Features: resolved, ImageBuilt: true}
	entries := imageMetadataEntries(req, map[string]string{metadata.Label: value}, nil)
	if phases := metadata.Plan(entries); len(phases) != 2 || phases[0].Command.Shell != "echo feature" || phases[1].Command.Shell != "echo config" {
		t.Fatalf("duplicated built-image hooks=%+v", phases)
	}
	if got, _ := metadata.Encode(entries); got != value {
		t.Fatalf("derived container metadata=%s, want the image label %s", got, value)
	}
	req.ImageBuilt = false
	if got := metadata.Plan(imageMetadataEntries(req, map[string]string{metadata.Label: value}, nil)); len(got) != 4 {
		t.Fatalf("prebuilt contributions unexpectedly removed: %+v", got)
	}
	req.ImageBuilt = true
	if got := metadata.Plan(imageMetadataEntries(req, nil, nil)); len(got) != 2 {
		t.Fatalf("missing-label fallback=%+v", got)
	}
}

func TestRuntimePlanSkipAndSubstitute(t *testing.T) {
	entries := []metadata.Entry{{ID: "base", PostCreate: spec.Command{Args: []string{"echo", "${localEnv:GREETING} ${containerEnv:SAMPLE}"}}, PostAttach: spec.Command{Shell: "echo attach"}}}
	for _, tc := range []struct {
		name string
		req  UpRequest
		want int
	}{
		{"all", UpRequest{}, 2}, {"skip-all", UpRequest{SkipLifecycle: true}, 0}, {"skip-attach", UpRequest{SkipPostAttach: true}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := runtimePlan(tc.req, entries, substituteCtxForTest(), map[string]string{"SAMPLE": "container"})
			if len(plan) != tc.want {
				t.Fatalf("plan=%+v", plan)
			}
			if len(plan) > 0 && (plan[0].Command.Args[1] != "hello container" || plan[0].Origin.Source != "base") {
				t.Fatalf("substitution=%+v", plan)
			}
		})
	}
}

func TestRuntimePlanSubstitutesEveryCommandForm(t *testing.T) {
	entries := []metadata.Entry{{PostCreate: spec.Command{Shell: "${localEnv:GREETING} ${containerEnv:SAMPLE}"}, PostStart: spec.Command{Parallel: map[string]spec.Command{"one": {Args: []string{"echo", "${localEnv:GREETING} ${containerEnv:SAMPLE}"}}, "two": {Shell: "${localEnv:GREETING} ${containerEnv:SAMPLE}"}}}}}
	plan := runtimePlan(UpRequest{}, entries, substituteCtxForTest(), map[string]string{"SAMPLE": "container"})
	if len(plan) != 2 || plan[0].Command.Shell != "hello container" || plan[1].Command.Parallel["one"].Args[1] != "hello container" || plan[1].Command.Parallel["two"].Shell != "hello container" {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestFeatureContainerEnvRetainsSpecPrecedence(t *testing.T) {
	resolved := []features.Resolved{{Metadata: features.Metadata{ContainerEnv: map[string]string{"A": "one", "B": "${localEnv:GREETING}", "C": "one"}}}, {Metadata: features.Metadata{ContainerEnv: map[string]string{"A": "two"}}}}
	got := featureContainerEnv(map[string]any{"C": "config"}, resolved, substituteCtxForTest())
	if !reflect.DeepEqual(got, map[string]any{"A": "two", "B": "hello", "C": "config"}) {
		t.Fatalf("env=%v", got)
	}
}

func TestCreateEmptyMetadataByImagePath(t *testing.T) {
	for _, built := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct-image", true: "built-image"}[built], func(t *testing.T) {
			h, created := fakeDaemon(t, `{}`)
			dc := &spec.DevContainer{Image: "alpine"}
			req := UpRequest{Spec: dc, Image: "alpine", ImageBuilt: built}
			req.metadataEntries = imageMetadataEntries(req, nil, func(string, ...any) { t.Fatal("absent metadata logged an error") })
			if len(req.metadataEntries) != 0 {
				t.Fatal(req.metadataEntries)
			}
			if _, err := h.create(context.Background(), dc, req, "/workspace"); err != nil {
				t.Fatal(err)
			}
			value, present := created.Labels[metadata.Label]
			if built && present || !built && (!present || value != "[]") {
				t.Fatalf("metadata label=%q present=%v built=%v", value, present, built)
			}
		})
	}
}
