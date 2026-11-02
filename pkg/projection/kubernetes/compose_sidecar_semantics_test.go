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
)

func sidecarRequest() ComposeProjectRequest {
	r := composeCommandRequest()
	r.Project.Services["worker"] = composetypes.ServiceConfig{Name: "worker", ContainerSpec: composetypes.ContainerSpec{Image: "alpine"}}
	return r
}

func TestComposeSidecarCommandForms(t *testing.T) {
	forms := []struct {
		name  string
		value []string
	}{{"nil", nil}, {"empty", []string{}}, {"empty-string-entry", []string{""}}, {"explicit", []string{"/program", "two words", "", "$(VALUE):$$"}}}
	for _, entry := range forms {
		for _, cmd := range forms {
			t.Run(entry.name+"/"+cmd.name, func(t *testing.T) {
				r := sidecarRequest()
				svc := r.Project.Services["worker"]
				svc.Entrypoint = entry.value
				svc.Command = cmd.value
				r.Project.Services["worker"] = svc
				image := &oci.ImageConfig{Entrypoint: []string{"/image", "$(VALUE)"}, Cmd: []string{"image-argument"}}
				r.SidecarImageConfigs = map[string]*oci.ImageConfig{"worker": image}
				out, err := ProjectCompose(r)
				if entry.value != nil && (len(entry.value) == 0 || (len(entry.value) == 1 && entry.value[0] == "")) && len(cmd.value) == 0 {
					if err == nil || !strings.Contains(err.Error(), `service "worker"`) || !strings.Contains(err.Error(), "no command specified") {
						t.Fatalf("error=%v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				c := out.Pod.Spec.Containers[1]
				// Serialize through the public manifest boundary: empty lists disappear.
				wire, _ := json.Marshal(c)
				if err := json.Unmarshal(wire, &c); err != nil {
					t.Fatal(err)
				}
				wantEntry, wantCmd := entry.value, cmd.value
				if len(wantEntry) == 1 && wantEntry[0] == "" {
					wantEntry = []string{}
				}
				if wantEntry == nil {
					wantEntry = image.Entrypoint
				}
				if wantCmd == nil && entry.value == nil {
					wantCmd = image.Cmd
				}
				want := append(append([]string{}, wantEntry...), wantCmd...)
				// Native CRI fallback, followed by kubelet's dollar unescaping of authored fields.
				gotEntry, gotCmd := c.Command, c.Args
				for i := range gotEntry {
					gotEntry[i] = strings.ReplaceAll(gotEntry[i], "$$", "$")
				}
				for i := range gotCmd {
					gotCmd[i] = strings.ReplaceAll(gotCmd[i], "$$", "$")
				}
				if len(gotEntry) == 0 {
					if len(gotCmd) == 0 {
						gotCmd = image.Cmd
					}
					gotEntry = image.Entrypoint
				}
				got := append(append([]string{}, gotEntry...), gotCmd...)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("argv=%q want=%q", got, want)
				}
				if !reflect.DeepEqual([]string(svc.Entrypoint), entry.value) || !reflect.DeepEqual([]string(svc.Command), cmd.value) {
					t.Fatal("service mutated")
				}
			})
		}
	}
}

func TestComposeSidecarEmptyCommandRequiresImage(t *testing.T) {
	for _, image := range []*oci.ImageConfig{nil, {}, {Entrypoint: []string{""}}} {
		r := sidecarRequest()
		svc := r.Project.Services["worker"]
		svc.Command = []string{}
		r.Project.Services["worker"] = svc
		r.SidecarImageConfigs = map[string]*oci.ImageConfig{"worker": image}
		if _, err := ProjectCompose(r); err == nil || !strings.Contains(err.Error(), `service "worker"`) {
			t.Fatalf("image=%+v error=%v", image, err)
		}
	}
}

func TestComposeSidecarEnvironment(t *testing.T) {
	for _, mode := range []string{"missing", "owned", "absent", "empty"} {
		t.Run(mode, func(t *testing.T) {
			r := sidecarRequest()
			svc := r.Project.Services["worker"]
			literal := "$(VALUE):${VALUE}:$$"
			empty := ""
			svc.Environment = composetypes.MappingWithEquals{"LITERAL": &literal, "EMPTY": &empty, "UNSET": nil}
			r.Project.Services["worker"] = svc
			image := &oci.ImageConfig{}
			if mode == "owned" || mode == "empty" {
				image.Env = []string{"UNSET=image"}
			}
			if mode != "missing" {
				r.SidecarImageConfigs = map[string]*oci.ImageConfig{"worker": image}
			}
			if mode == "empty" {
				svc.Environment["UNSET"] = &empty
			}
			out, err := ProjectCompose(r)
			if mode == "missing" || mode == "owned" {
				if err == nil || !strings.Contains(err.Error(), `service "worker"`) || !strings.Contains(err.Error(), `"UNSET"`) {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			c := out.Pod.Spec.Containers[1]
			env := map[string]string{}
			for _, e := range c.Env {
				env[e.Name] = e.Value
			}
			if env["LITERAL"] != "$$(VALUE):$${VALUE}:$$$$" {
				t.Fatalf("literal=%q", env["LITERAL"])
			}
			if v, ok := env["EMPTY"]; !ok || v != "" {
				t.Fatalf("empty=%q present=%v", v, ok)
			}
			if _, ok := env["UNSET"]; ok != (mode == "empty") {
				t.Fatalf("unset present=%v", ok)
			}
			if *svc.Environment["LITERAL"] != literal {
				t.Fatal("environment mutated")
			}
		})
	}
}

func TestComposeSidecarLoaderForms(t *testing.T) {
	for _, field := range []string{"entrypoint", "command"} {
		for _, form := range []string{"null", "[]", `""`, `[""]`, `["/program", "two words", ""]`, `'/program "two words"'`} {
			t.Run(field+"/"+form, func(t *testing.T) {
				dc, p, _ := loadProjectForTest(t, "services:\n  app:\n    image: alpine\n  worker:\n    image: alpine\n    "+field+": "+form+"\n", `{"dockerComposeFile":"compose.yml","service":"app"}`)
				r := sidecarRequest()
				r.Spec = dc
				r.Project = p
				r.SidecarImageConfigs = map[string]*oci.ImageConfig{"worker": {Entrypoint: []string{"/image"}, Cmd: []string{"arg"}}}
				out, err := ProjectCompose(r)
				empty := form == "[]" || form == `""`
				if field == "entrypoint" && (empty || form == `[""]`) {
					if err == nil {
						t.Fatal("empty entrypoint retained image command")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				c := out.Pod.Spec.Containers[1]
				if field == "command" && empty && !reflect.DeepEqual(c.Command, []string{"/image"}) {
					t.Fatalf("command=%q", c.Command)
				}
				if form == "null" && (len(c.Command) != 0 || len(c.Args) != 0) {
					t.Fatalf("null overrode image: %+v", c)
				}
			})
		}
	}
}
