package builder

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"github.com/wyrd-company/devcontainer-builder/pkg/platform"
)

func TestDockerfileUser(t *testing.T) {
	images := map[string]oci.ImageConfig{}
	image := func(name, user string, env ...string) {
		var cfg oci.ImageConfig
		cfg.Config.User, cfg.Config.Env = user, env
		images[name] = cfg
	}
	image("base:1", "image-user", "IMAGE_ACCOUNT=from-image")
	image("base:2", "two-user")
	image("base:3", "three-user")
	image("base:4", "four-user")

	cases := []struct {
		name       string
		dockerfile string
		target     string
		buildArgs  map[string]string
		want       string
		wantLoads  string // images whose config was read
	}{
		{"last USER wins", "FROM base:1\nUSER first\nRUN true\nUSER second\n", "", nil, "second", ""},
		{"no USER uses the image user", "FROM base:1\nRUN true\n", "", nil, "image-user", "base:1"},
		{"scratch has no user", "FROM scratch\n", "", nil, "", ""},
		{"base image from global ARG", "ARG BASE=base:2\nFROM $BASE\n", "", nil, "two-user", "base:2"},
		{"build arg overrides global ARG", "ARG BASE=base:2\nFROM $BASE\n", "", map[string]string{"BASE": "base:3"}, "three-user", "base:3"},
		{"global ARG default expands an earlier global ARG", "ARG V=4\nARG BASE=base:${V}\nFROM $BASE\n", "", nil, "four-user", "base:4"},
		{"USER from stage ARG default", "FROM base:1\nARG NAME=arg-user\nUSER $NAME\n", "", nil, "arg-user", ""},
		{"USER from build arg", "FROM base:1\nARG NAME=arg-user\nUSER $NAME\n", "", map[string]string{"NAME": "build-user"}, "build-user", ""},
		{"ARG default expands an earlier ARG", "FROM base:1\nARG ACCOUNT=nobody\nARG CHOSEN=${ACCOUNT}\nUSER ${CHOSEN}\n", "", nil, "nobody", ""},
		{"build arg feeds a dependent ARG default", "FROM base:1\nARG ACCOUNT=nobody\nARG CHOSEN=${ACCOUNT}\nUSER ${CHOSEN}\n", "", map[string]string{"ACCOUNT": "built"}, "built", ""},
		{"ARG without default takes the global value", "ARG NAME=global-user\nFROM base:1\nARG NAME\nUSER $NAME\n", "", nil, "global-user", ""},
		{"global ARG is not in stage scope undeclared", "ARG NAME=global-user\nFROM base:1\nUSER x${NAME}\n", "", nil, "x", "base:1"},
		{"USER from ENV", "FROM base:1\nENV NAME=env-user\nUSER ${NAME}:1000\n", "", nil, "env-user:1000", ""},
		{"USER from the image env", "FROM base:1\nUSER ${IMAGE_ACCOUNT}\n", "", nil, "from-image", "base:1"},
		{"ENV inherited from the parent stage", "FROM base:1 AS parent\nENV ACCOUNT=nobody\nFROM parent\nUSER ${ACCOUNT}\n", "", nil, "nobody", ""},
		{"image env inherited through the parent stage", "FROM base:1 AS parent\nRUN true\nFROM parent\nUSER ${IMAGE_ACCOUNT}\n", "", nil, "from-image", "base:1"},
		{"USER inherited from the parent stage", "FROM base:1 AS one\nUSER stage-user\nFROM one AS two\nRUN true\n", "", nil, "stage-user", ""},
		{"target stage", "FROM base:1 AS one\nUSER one-user\nFROM base:1 AS two\nUSER two-user\n", "one", nil, "one-user", ""},
		{"chain without USER uses its image", "FROM base:4 AS one\nFROM one\n", "", nil, "four-user", "base:4"},
		{"automatic platform ARG", "FROM base:1\nARG TARGETARCH\nUSER u-${TARGETARCH}\n", "", nil, "u-arm64", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "Dockerfile")
			writeFile(t, path, c.dockerfile)
			var loads []string
			load := func(name string) (*oci.ImageConfig, error) {
				loads = append(loads, name)
				cfg, ok := images[name]
				if !ok {
					return nil, fmt.Errorf("no image %s", name)
				}
				return &cfg, nil
			}
			got, err := dockerfileUser(path, c.target, c.buildArgs, platform.Platform{OS: "linux", Arch: "arm64"}, load)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("user = %q, want %q", got, c.want)
			}
			if l := strings.Join(loads, ","); l != c.wantLoads {
				t.Errorf("image configs read = %q, want %q", l, c.wantLoads)
			}
		})
	}

	failing := func(string) (*oci.ImageConfig, error) { return nil, fmt.Errorf("registry down") }
	path := filepath.Join(t.TempDir(), "Dockerfile")
	writeFile(t, path, "FROM base:1\nUSER ${IMAGE_ACCOUNT}\n")
	if _, err := dockerfileUser(path, "", nil, platform.Current(), failing); err == nil || !strings.Contains(err.Error(), "registry down") {
		t.Fatalf("expected the image config error when the image env is needed, got %v", err)
	}
	writeFile(t, path, "FROM base:1 AS one\n")
	if _, err := dockerfileUser(path, "missing", nil, platform.Current(), failing); err == nil {
		t.Fatal("expected error for a missing target stage")
	}
}
