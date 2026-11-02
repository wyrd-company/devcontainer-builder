// relationships:
//
//	implements: schema-conformance
package kubernetes

import (
	"reflect"
	"strings"
	"testing"

	oci "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestProjectImageCommand(t *testing.T) {
	for _, mode := range []string{"default", "true", "false"} {
		for _, commands := range []struct {
			name       string
			entry, cmd []string
		}{
			{"empty", nil, nil}, {"entry", []string{"/entry", "two words"}, nil},
			{"cmd", nil, []string{"/command", ""}}, {"both", []string{"/entry"}, []string{"argument", "two words", "", "$(COLLISION)", "$$"}},
		} {
			t.Run(mode+"/"+commands.name, func(t *testing.T) {
				dc := &spec.DevContainer{}
				if mode != "default" {
					v := mode == "true"
					dc.OverrideCommand = &v
				}
				cfg := &oci.ImageConfig{Entrypoint: commands.entry, Cmd: commands.cmd, Env: []string{"BASE=retained"}}
				before := *cfg
				pj, err := Project(ProjectRequest{Spec: dc, Image: "alpine", ImageConfig: cfg, Features: []features.Resolved{
					{Metadata: features.Metadata{Entrypoint: "echo first"}}, {Metadata: features.Metadata{}}, {Metadata: features.Metadata{Entrypoint: "echo second"}},
				}})
				if err != nil {
					t.Fatal(err)
				}
				c := pj.Pod.Spec.Containers[0]
				want := []string{"-c", strings.Replace(keepaliveCmd, `exec "$@"`, "echo first\necho second\n"+`exec "$@"`, 1), "-"}
				if mode == "false" {
					want = append(want, commands.entry...)
					want = append(want, commands.cmd...)
				}
				for i := range want {
					want[i] = strings.ReplaceAll(want[i], "$", "$$")
				}
				if !reflect.DeepEqual(c.Command, []string{"/bin/sh"}) || !reflect.DeepEqual(c.Args, want) {
					t.Fatalf("command=%q args=%q want=%q", c.Command, c.Args, want)
				}
				if !reflect.DeepEqual(*cfg, before) {
					t.Fatal("input image configuration mutated")
				}
			})
		}
	}
}

func TestProjectImageCommandMissingConfig(t *testing.T) {
	v := false
	pj, err := Project(ProjectRequest{Spec: &spec.DevContainer{OverrideCommand: &v}, Image: "alpine"})
	if pj != nil || err == nil || !strings.Contains(err.Error(), "ImageConfig is required when overrideCommand is false") {
		t.Fatalf("projection=%v err=%v", pj, err)
	}
	// Default/true needs no image inspection.
	for _, override := range []*bool{nil, boolPtr(true)} {
		if _, err := Project(ProjectRequest{Spec: &spec.DevContainer{OverrideCommand: override}, Image: "alpine"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProjectEnvironmentPrecedence(t *testing.T) {
	cfg := &oci.ImageConfig{Env: []string{"BASE=base", "COLLISION=image"}}
	dc := &spec.DevContainer{ContainerEnv: map[string]any{"COLLISION": "config", "EMPTY": "", "LITERAL": "$(BASE):${BASE}:$$"}}
	f := []features.Resolved{
		{Metadata: features.Metadata{ContainerEnv: map[string]string{"COLLISION": "first", "FEATURE": "first", "EMPTY": "first"}}},
		{Metadata: features.Metadata{ContainerEnv: map[string]string{"COLLISION": "last", "FEATURE": "last"}}},
	}
	pj, err := Project(ProjectRequest{Spec: dc, Image: "alpine", ImageConfig: cfg, Features: f})
	if err != nil {
		t.Fatal(err)
	}
	c := pj.Pod.Spec.Containers[0]
	got := envAsMap(c.Env)
	want := map[string]string{"COLLISION": "config", "FEATURE": "last", "EMPTY": "", "LITERAL": "$$(BASE):$${BASE}:$$$$"}
	if !reflect.DeepEqual(got, want) || len(c.Env) != len(want) {
		t.Fatalf("env=%v want=%v", c.Env, want)
	}
	if cfg.Env[1] != "COLLISION=image" || dc.ContainerEnv["COLLISION"] != "config" || f[0].Metadata.ContainerEnv["COLLISION"] != "first" {
		t.Fatal("input mutated")
	}
}
