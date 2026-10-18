package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/distribution/reference"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/frontend/dockerfile/dockerfile2llb"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"github.com/wyrd-company/devcontainer-builder/pkg/platform"
)

// diffImages are the base images the differential corpus builds on.
var diffImages = map[string]oci.ImageConfig{
	"base:1": imageCfg("image-user", "IMAGE_ACCOUNT=from-image", "SHARED=image"),
	"base:2": imageCfg("two-user"),
}

func imageCfg(user string, env ...string) oci.ImageConfig {
	var cfg oci.ImageConfig
	cfg.Config.User, cfg.Config.Env = user, env
	return cfg
}

func normalized(t *testing.T, name string) string {
	t.Helper()
	ref, err := reference.ParseNormalizedNamed(name)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return reference.TagNameOnly(ref).String()
}

// imageResolver serves diffImages to dockerfile2llb.
type imageResolver struct{ t *testing.T }

func (r imageResolver) ResolveImageConfig(_ context.Context, ref string, _ sourceresolver.Opt) (string, digest.Digest, []byte, error) {
	for name, cfg := range diffImages {
		if normalized(r.t, name) == ref {
			dt, err := json.Marshal(cfg)
			if err != nil {
				return "", "", nil, err
			}
			return ref, digest.FromBytes(dt), dt, nil
		}
	}
	return "", "", nil, fmt.Errorf("no image %s", ref)
}

// buildkitUser converts the Dockerfile with BuildKit's own frontend and
// returns the USER of the resulting image config.
func buildkitUser(t *testing.T, dockerfile, target string, buildArgs map[string]string, p ocispecs.Platform) string {
	t.Helper()
	_, img, _, _, err := dockerfile2llb.Dockerfile2LLB(context.Background(), []byte(dockerfile), dockerfile2llb.ConvertOpt{
		Config:         dockerui.Config{BuildArgs: buildArgs, Target: target},
		TargetPlatform: &p,
		MetaResolver:   imageResolver{t},
	})
	if err != nil {
		t.Fatalf("dockerfile2llb: %v", err)
	}
	return img.Config.User
}

// TestDockerfileUser_AgreesWithDockerfile2LLB checks the evaluator
// against BuildKit's Dockerfile frontend on a corpus of ARG chains,
// multi-stage ENV inheritance, multi-assignment ENV and USER with
// variables.
func TestDockerfileUser_AgreesWithDockerfile2LLB(t *testing.T) {
	corpus := []struct {
		name       string
		dockerfile string
		target     string
		buildArgs  map[string]string
	}{
		// ARG chains
		{"global ARG chain in FROM", "ARG V=1\nARG BASE=base:${V}\nFROM $BASE\n", "", nil},
		{"global ARG build arg", "ARG V=1\nARG BASE=base:${V}\nFROM $BASE\n", "", map[string]string{"V": "2"}},
		{"stage ARG chain", "FROM base:1\nARG A=a-user\nARG B=${A}\nARG C=${B:-unset}-x\nUSER $C\n", "", nil},
		{"stage ARG chain with build arg", "FROM base:1\nARG A=a-user\nARG B=${A}\nUSER $B\n", "", map[string]string{"A": "built"}},
		{"ARGs in one instruction", "FROM base:1\nARG A=one B=${A}-two\nUSER $B\n", "", nil},
		{"valueless ARG takes global", "ARG NAME=global-user\nFROM base:1\nARG NAME\nUSER $NAME\n", "", nil},
		{"undeclared global is out of scope", "ARG NAME=global-user\nFROM base:1\nUSER x${NAME}y\n", "", nil},
		{"ARG shadowed by later ENV", "FROM base:1\nARG N=arg-user\nENV N=env-user\nUSER $N\n", "", nil},
		{"ENV shadowed by later ARG", "FROM base:1\nENV N=env-user\nARG N=arg-user\nUSER $N\n", "", nil},
		{"automatic platform ARG", "FROM base:1\nARG TARGETARCH\nUSER u-${TARGETARCH}\n", "", nil},
		// Multi-stage ENV inheritance
		{"parent ENV", "FROM base:1 AS parent\nENV ACCOUNT=nobody\nFROM parent\nUSER ${ACCOUNT}\n", "", nil},
		{"grandparent ENV", "FROM base:1 AS one\nENV A=first\nFROM one AS two\nENV B=${A}-second\nFROM two\nUSER $B\n", "", nil},
		{"parent ARG is visible", "FROM base:1 AS parent\nARG P=parent-arg\nFROM parent\nUSER $P\n", "", nil},
		{"image env through stages", "FROM base:1 AS parent\nRUN true\nFROM parent\nUSER ${IMAGE_ACCOUNT}\n", "", nil},
		{"parent USER inherited", "FROM base:1 AS one\nUSER stage-user\nFROM one\nRUN true\n", "", nil},
		{"target stage", "FROM base:1 AS one\nUSER one-user\nFROM base:2 AS two\nUSER two-user\n", "one", nil},
		{"no USER takes image user", "FROM base:2 AS one\nFROM one\n", "", nil},
		{"scratch", "FROM scratch\nENV X=1\n", "", nil},
		// Multi-assignment ENV
		{"ENV expands before assigning", "FROM base:1\nENV ACCOUNT=old-user\nENV ACCOUNT=new-user CHOSEN=${ACCOUNT}\nUSER ${CHOSEN}\n", "", nil},
		{"ENV sees image env, not its own", "FROM base:1\nENV SHARED=mine COPY=${SHARED}\nUSER $COPY\n", "", nil},
		{"ENV key expansion", "FROM base:1\nENV NAME=dyn\nENV ${NAME}=key-user\nUSER $dyn\n", "", nil},
		{"ENV legacy form", "FROM base:1\nENV ACCOUNT legacy user\nUSER \"$ACCOUNT\"\n", "", nil},
		// USER with variables
		{"USER with uid:gid", "FROM base:1\nENV U=app\nUSER ${U}:1000\n", "", nil},
		{"USER default operator", "FROM base:1\nUSER ${MISSING:-fallback-user}\n", "", nil},
		{"USER alternate operator", "FROM base:1\nENV SET=1\nUSER ${SET:+set-user}\n", "", nil},
		{"USER from image env", "FROM base:1\nUSER ${IMAGE_ACCOUNT}\n", "", nil},
		{"USER escaped dollar", "FROM base:1\nUSER \\$literal\n", "", nil},
		{"last USER wins", "FROM base:1\nUSER first\nUSER second\n", "", nil},
	}
	p := platform.Platform{OS: "linux", Arch: "arm64"}
	load := func(name string) (*oci.ImageConfig, error) {
		cfg, ok := diffImages[name]
		if !ok {
			return nil, fmt.Errorf("no image %s", name)
		}
		return &cfg, nil
	}
	for _, c := range corpus {
		t.Run(c.name, func(t *testing.T) {
			want := buildkitUser(t, c.dockerfile, c.target, c.buildArgs, ocispecs.Platform{OS: p.OS, Architecture: p.Arch})
			path := filepath.Join(t.TempDir(), "Dockerfile")
			writeFile(t, path, c.dockerfile)
			got, err := dockerfileUser(path, c.target, c.buildArgs, p, load)
			if err != nil {
				t.Fatalf("dockerfileUser: %v", err)
			}
			if got != want {
				t.Fatalf("user = %q, dockerfile2llb = %q", got, want)
			}
		})
	}
}
