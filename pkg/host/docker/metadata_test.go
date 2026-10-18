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
	cfg, host, err := buildContainerConfigWithEntrypoints(dc, nil, entrypoints, "alpine", "/workspace", "", "")
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
	_, host, err := buildContainerConfig(&spec.DevContainer{}, resolved, "alpine", "/workspace", "", "")
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
