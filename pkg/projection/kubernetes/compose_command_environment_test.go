// relationships:
//
//	implements: schema-conformance
package kubernetes

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	oci "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func composeCommandRequest() ComposeProjectRequest {
	return ComposeProjectRequest{Spec: &spec.DevContainer{DockerComposeFile: spec.StringOrSlice{"compose.yml"}, Service: "app"}, Project: &composetypes.Project{Name: "sample", Services: composetypes.Services{"app": {Name: "app", ContainerSpec: composetypes.ContainerSpec{Image: "alpine"}}}}, PrimaryImageConfig: &oci.ImageConfig{Entrypoint: []string{"/image-entry"}, Cmd: []string{"image-cmd"}}}
}

func TestComposeCommandLoaderForms(t *testing.T) {
	for _, field := range []string{"entrypoint", "command"} {
		for _, form := range []string{"absent", "null", "[]", `""`, `["/service", "two words", ""]`, `'/service "two words"'`} {
			for _, mode := range []string{"default", "false", "true"} {
				t.Run(field+"/"+form+"/"+mode, func(t *testing.T) {
					body := "services:\n  app:\n    image: alpine\n"
					if form != "absent" {
						body += "    " + field + ": " + form + "\n"
					}
					config := `{"dockerComposeFile":"compose.yml","service":"app"`
					if mode != "default" {
						config += `,"overrideCommand":` + mode
					}
					config += "}"
					dc, p, _ := loadProjectForTest(t, body, config)
					r := composeCommandRequest()
					r.Spec = dc
					r.Project = p
					out, err := ProjectCompose(r)
					if err != nil {
						t.Fatal(err)
					}
					var value []string
					switch form {
					case "absent", "null":
						value = nil
					case "[]", `""`:
						value = []string{}
					case `'/service "two words"'`:
						value = []string{"/service", "two words"}
					default:
						value = []string{"/service", "two words", ""}
					}
					svc := p.Services["app"]
					got := svc.Command
					if field == "entrypoint" {
						got = svc.Entrypoint
					}
					if !reflect.DeepEqual([]string(got), value) {
						t.Fatalf("loader value=%#v want=%#v", got, value)
					}
					want := []string{}
					if mode != "true" {
						entry, cmd := r.PrimaryImageConfig.Entrypoint, r.PrimaryImageConfig.Cmd
						if field == "entrypoint" && value != nil {
							entry = value
							cmd = nil
						}
						if field == "command" && value != nil {
							cmd = value
						}
						want = append(want, entry...)
						want = append(want, cmd...)
					}
					args := out.Pod.Spec.Containers[0].Args
					if !reflect.DeepEqual(args[3:], want) {
						t.Fatalf("argv=%q want=%q", args[3:], want)
					}
				})
			}
		}
	}
}

func TestComposeCommandMissingImage(t *testing.T) {
	for _, mode := range []string{"default", "false", "true"} {
		t.Run(mode, func(t *testing.T) {
			r := composeCommandRequest()
			r.PrimaryImageConfig = nil
			if mode != "default" {
				r.Spec.OverrideCommand = boolPtr(mode == "true")
			}
			out, err := ProjectCompose(r)
			if mode == "true" {
				if err != nil {
					t.Fatal(err)
				}
				if len(out.Pod.Spec.Containers[0].Args) != 3 {
					t.Fatal("override retained command")
				}
				return
			}
			if out != nil || err == nil || !strings.Contains(err.Error(), `primary service "app": PrimaryImageConfig is required for image entrypoint fallback`) {
				t.Fatalf("out=%v err=%v", out, err)
			}
		})
	}
	// Explicit entrypoint suppresses image Cmd, including an empty entrypoint.
	for _, entry := range []composetypes.ShellCommand{{}, {"/service"}} {
		r := composeCommandRequest()
		r.PrimaryImageConfig = nil
		s := r.Project.Services["app"]
		s.Entrypoint = entry
		r.Project.Services["app"] = s
		if _, err := ProjectCompose(r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestComposeEnvironmentPrecedenceAndNonmutation(t *testing.T) {
	r := composeCommandRequest()
	r.PrimaryImageConfig.Env = []string{"BASE=image", "COLLISION=image"}
	s := r.Project.Services["app"]
	s.Command = []string{"$(BASE)", "$$", "two words", ""}
	s.Environment = composetypes.MappingWithEquals{"COLLISION": stringPtr("service"), "SERVICE": stringPtr("$(BASE):$$"), "EMPTY": stringPtr("service"), "REPLACED": nil}
	r.Project.Services["app"] = s
	r.Spec.ContainerEnv = map[string]any{"COLLISION": "config", "EMPTY": "", "REPLACED": "config"}
	r.Features = []features.Resolved{{Metadata: features.Metadata{Entrypoint: "echo first", ContainerEnv: map[string]string{"COLLISION": "first", "FEATURE": "first"}}}, {Metadata: features.Metadata{Entrypoint: "echo second", ContainerEnv: map[string]string{"COLLISION": "last", "FEATURE": "last"}}}}
	before, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ProjectCompose(r)
	if err != nil {
		t.Fatal(err)
	}
	c := out.Pod.Spec.Containers[0]
	want := map[string]string{"COLLISION": "config", "FEATURE": "last", "EMPTY": "", "SERVICE": "$$(BASE):$$$$", "REPLACED": "config"}
	if !reflect.DeepEqual(envAsMap(c.Env), want) || len(c.Env) != len(want) {
		t.Fatalf("env=%v want=%v", c.Env, want)
	}
	if !strings.Contains(c.Args[1], "echo first\necho second\nexec") {
		t.Fatalf("feature wrapper=%q", c.Args[1])
	}
	if !reflect.DeepEqual(c.Args[3:], []string{"/image-entry", "$$(BASE)", "$$$$", "two words", ""}) {
		t.Fatalf("args=%q", c.Args)
	}
	after, _ := json.Marshal(r)
	if string(before) != string(after) {
		t.Fatalf("request mutated\nbefore=%s\nafter=%s", before, after)
	}
}

func TestComposeEnvironmentUnset(t *testing.T) {
	for _, tc := range []struct {
		name            string
		image           *oci.ImageConfig
		feature, config string
		wantError       string
	}{
		{name: "unknown", wantError: "PrimaryImageConfig is required for unset environment key"},
		{name: "absent", image: &oci.ImageConfig{}},
		{name: "owned", image: &oci.ImageConfig{Env: []string{"VALUE=image"}}, wantError: "no Kubernetes equivalent"},
		{name: "owned-empty", image: &oci.ImageConfig{Env: []string{"VALUE="}}, wantError: "no Kubernetes equivalent"},
		{name: "prefix", image: &oci.ImageConfig{Env: []string{"VALUE_OTHER=image"}}},
		{name: "feature", feature: "feature"},
		{name: "config", image: &oci.ImageConfig{Env: []string{"VALUE=image"}}, feature: "feature", config: "config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := composeCommandRequest()
			r.PrimaryImageConfig = tc.image
			r.Spec.OverrideCommand = boolPtr(true)
			s := r.Project.Services["app"]
			s.Environment = composetypes.MappingWithEquals{"VALUE": nil}
			r.Project.Services["app"] = s
			if tc.feature != "" {
				r.Features = []features.Resolved{{Metadata: features.Metadata{ContainerEnv: map[string]string{"VALUE": tc.feature}}}}
			}
			if tc.config != "" {
				r.Spec.ContainerEnv = map[string]any{"VALUE": tc.config}
			}
			out, err := ProjectCompose(r)
			if tc.wantError != "" {
				if out != nil || err == nil || !strings.Contains(err.Error(), tc.wantError) || !strings.Contains(err.Error(), `primary service "app"`) {
					t.Fatalf("out=%v err=%v", out, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			env := envAsMap(out.Pod.Spec.Containers[0].Env)
			want := tc.feature
			if tc.config != "" {
				want = tc.config
			}
			if want == "" {
				if _, ok := env["VALUE"]; ok {
					t.Fatal("unset emitted")
				}
			} else if env["VALUE"] != want {
				t.Fatalf("env=%v", env)
			}
		})
	}
}
