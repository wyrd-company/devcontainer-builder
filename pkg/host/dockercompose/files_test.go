// relationships:
//
//	references: [files, project]
package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	dockerclient "github.com/docker/docker/client"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestComposeDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name, env, dotenv   string
		override, directory bool
		want                []string
	}{
		{name: "default", want: []string{"docker-compose.yml"}},
		{name: "override", override: true, want: []string{"docker-compose.yml", "docker-compose.override.yml"}},
		{name: "override-directory", directory: true, want: []string{"docker-compose.yml"}},
		{name: "environment", env: "a.yml" + string(os.PathListSeparator) + "b.yml", dotenv: "COMPOSE_FILE=ignored.yml", want: []string{"a.yml", "b.yml"}},
		{name: "dotenv", dotenv: "COMPOSE_FILE= a.yml ", want: []string{"a.yml"}},
		{name: "literal-quotes", dotenv: "COMPOSE_FILE=\"a.yml\"", want: []string{`"a.yml"`}},
		{name: "first-match", dotenv: "COMPOSE_FILE=a.yml\nCOMPOSE_FILE=b.yml", want: []string{"a.yml"}},
		{name: "export-ignored", dotenv: "export COMPOSE_FILE=a.yml", want: []string{"docker-compose.yml"}},
		{name: "blank", dotenv: "COMPOSE_FILE=  ", want: []string{"docker-compose.yml"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			dc := &spec.DevContainer{Path: filepath.Join(dir, ".devcontainer", "devcontainer.json"), DockerComposeFile: spec.StringOrSlice{}, Service: "app"}
			if tc.dotenv != "" {
				if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(tc.dotenv), 0600); err != nil {
					t.Fatal(err)
				}
			}
			override := filepath.Join(dir, "docker-compose.override.yml")
			if tc.override {
				if err := os.WriteFile(override, []byte("services: {}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.directory {
				if err := os.Mkdir(override, 0700); err != nil {
					t.Fatal(err)
				}
			}
			got, err := ResolveComposeFiles(dc, dir, map[string]string{"COMPOSE_FILE": tc.env})
			if err != nil {
				t.Fatal(err)
			}
			want := make([]string, len(tc.want))
			for i, p := range tc.want {
				want[i] = filepath.Join(dir, p)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %q want %q", got, want)
			}
			dc.DockerComposeFile = spec.StringOrSlice{"explicit.yml"}
			got, err = ResolveComposeFiles(dc, dir, map[string]string{"COMPOSE_FILE": "ignored.yml"})
			if err != nil || !reflect.DeepEqual(got, []string{filepath.Join(dir, ".devcontainer", "explicit.yml")}) {
				t.Fatalf("explicit %q %v", got, err)
			}
		})
	}
}
func TestComposeDiscoveryProjectFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(path, []byte("services:\n  app:\n    image: alpine\n"), 0600); err != nil {
		t.Fatal(err)
	}
	proj, err := LoadProject(context.Background(), LoadInputs{Spec: &spec.DevContainer{DockerComposeFile: spec.StringOrSlice{}, Service: "app"}, Workspace: dir, LocalEnv: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(proj.ComposeFiles, []string{path}) || proj.WorkingDir != dir {
		t.Fatalf("discovery project: %#v", proj)
	}
}

func TestComposeDiscoveryEnvErrors(t *testing.T) {
	dir := t.TempDir()
	dc := &spec.DevContainer{DockerComposeFile: spec.StringOrSlice{}, Service: "app"}
	if err := os.Mkdir(filepath.Join(dir, ".env"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveComposeFiles(dc, dir, nil); err != nil {
		t.Fatal(err)
	}
	// A non-directory workspace causes ENOTDIR, which upstream propagates.
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveComposeFiles(dc, file, nil); err == nil {
		t.Fatal("ENOTDIR swallowed")
	}
	modern := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(modern, nil, 0600); err != nil {
		t.Fatal(err)
	}
	files, err := ResolveComposeFiles(dc, dir, nil)
	if err != nil || len(files) != 1 || files[0] != filepath.Join(dir, "docker-compose.yml") {
		t.Fatalf("modern discovery invented: %q %v", files, err)
	}
}

func TestReaderEnvironmentProjectGuard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(path, []byte("services:\n  app:\n    image: alpine\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadProject(context.Background(), LoadInputs{Workspace: dir, LocalEnv: map[string]string{}, Spec: &spec.DevContainer{DockerComposeFile: spec.StringOrSlice{}, Service: "app", ContainerEnv: map[string]any{"VALUE": true}}})
	if err == nil || !strings.Contains(err.Error(), "containerEnv") {
		t.Fatalf("environment guard %v", err)
	}
}

// Reach container creation through Up so the recorded file list cannot silently
// fall back to the empty declaration after project loading and profile filtering.
func TestComposeDiscoveryRuntimeLabel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(path, []byte("services:\n  app:\n    image: alpine\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var labels map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			fmt.Fprint(w, `[]`)
		case strings.Contains(r.URL.Path, "/images/"):
			fmt.Fprint(w, `{"Id":"sample-image","Config":{}}`)
		case strings.HasSuffix(r.URL.Path, "/networks"):
			fmt.Fprint(w, `[]`)
		case strings.Contains(r.URL.Path, "/networks/"):
			fmt.Fprint(w, `{"Id":"sample-network","Name":"sample_default"}`)
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			var body struct{ Labels map[string]string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			labels = body.Labels
			http.Error(w, "captured create", 500)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer server.Close()
	cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(server.URL), dockerclient.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	h := New(cli, Options{})
	_, err = h.Up(context.Background(), UpRequest{Spec: &spec.DevContainer{DockerComposeFile: spec.StringOrSlice{}, Service: "app"}, LocalWorkspace: dir, LocalEnv: map[string]string{}, SkipLifecycle: true})
	if err == nil || !strings.Contains(err.Error(), "captured create") || labels[LabelConfigFiles] != path {
		t.Fatalf("create labels %v error %v", labels, err)
	}
}
