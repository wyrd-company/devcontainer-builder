// relationships:
//   references: [llb]

package buildkit

import (
	"context"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/platform"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/vscode"
)

// marshalGraph runs the LLB marshaller and returns a flattened text
// rendering of every Op in the definition. Useful to assert that the
// expected operations are present without depending on the exact
// digest layout.
func marshalGraph(t *testing.T, in GraphInputs) string {
	t.Helper()
	state, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	def, err := state.Marshal(context.Background())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var b strings.Builder
	for _, dt := range def.Def {
		b.Write(dt)
		b.WriteString("\n")
	}
	return b.String()
}

func TestBuild_BareImage(t *testing.T) {
	got := marshalGraph(t, GraphInputs{
		Spec:               &spec.DevContainer{Image: "alpine:3.20"},
		BaseImage:          "alpine:3.20",
		ResolveImageConfig: false,
	})
	if !strings.Contains(got, "docker.io/library/alpine:3.20") {
		t.Errorf("expected alpine reference in graph, got:\n%s", got)
	}
}

func TestBuild_RestoresBaseStateUser(t *testing.T) {
	for _, imageUser := range []string{"", "root", "sample", "1000:1001"} {
		t.Run("image-user="+imageUser, func(t *testing.T) {
			base := llb.Image("alpine:3.20").User(imageUser)
			in := featureGraphInputs(testFeature)
			in.BaseState = &base
			in.ImageUser = imageUser
			in.Spec.ContainerUser = "container-sample"
			in.Spec.WorkspaceFolder = "/workspace"
			state, err := Build(in)
			if err != nil {
				t.Fatal(err)
			}
			def, err := state.Run(llb.Args([]string{"id", "-u"})).Root().Marshal(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, op := range decodeOps(t, def.ToPB()) {
				if e := op.GetExec(); e != nil && len(e.Meta.Args) == 2 && e.Meta.Args[0] == "id" && e.Meta.Args[1] == "-u" {
					found = true
					if e.Meta.User != imageUser {
						t.Fatalf("final state user = %q, want base user %q", e.Meta.User, imageUser)
					}
				}
			}
			if !found {
				t.Fatal("missing id -u exec probe in final state")
			}
		})
	}
}

func TestNormalizeImageRef(t *testing.T) {
	got, err := normalizeImageRef("alpine:3.20")
	if err != nil {
		t.Fatalf("normalizeImageRef: %v", err)
	}
	if got != "docker.io/library/alpine:3.20" {
		t.Fatalf("normalizeImageRef = %q, want docker.io/library/alpine:3.20", got)
	}
}

// graphOps decodes a marshaled LLB definition into its ops, keyed by
// digest.
type graphOps map[digest.Digest]*pb.Op

func decodeGraph(t *testing.T, in GraphInputs) graphOps {
	t.Helper()
	state, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	def, err := state.Marshal(context.Background())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	ops := graphOps{}
	for _, dt := range def.Def {
		var op pb.Op
		if err := op.UnmarshalVT(dt); err != nil {
			t.Fatalf("decode op: %v", err)
		}
		ops[digest.FromBytes(dt)] = &op
	}
	return ops
}

// execWithArg returns the exec op whose shell script equals script.
func (g graphOps) execWithArg(t *testing.T, script string) *pb.Op {
	t.Helper()
	for _, op := range g {
		if e := op.GetExec(); e != nil && len(e.Meta.Args) == 3 && e.Meta.Args[2] == script {
			return op
		}
	}
	t.Fatalf("no exec runs %q", script)
	return nil
}

// mkfiles returns the Mkfile actions (path -> data) of the op feeding
// the exec mount at dest.
func (g graphOps) mkfiles(t *testing.T, exec *pb.Op, dest string) map[string]string {
	t.Helper()
	for _, m := range exec.GetExec().Mounts {
		if m.Dest != dest {
			continue
		}
		src := g[digest.Digest(exec.Inputs[m.Input].Digest)]
		out := map[string]string{}
		for _, a := range src.GetFile().GetActions() {
			if mk := a.GetMkfile(); mk != nil {
				out[mk.Path] = string(mk.Data)
			}
		}
		return out
	}
	t.Fatalf("exec has no mount at %s", dest)
	return nil
}

func (g graphOps) allMkfiles() map[string]string {
	out := map[string]string{}
	for _, op := range g {
		for _, a := range op.GetFile().GetActions() {
			if mk := a.GetMkfile(); mk != nil {
				out[mk.Path] = string(mk.Data)
			}
		}
	}
	return out
}

func featureGraphInputs(f features.Resolved) GraphInputs {
	return GraphInputs{
		Spec: &spec.DevContainer{
			Image:        "alpine",
			RemoteUser:   "remote-user",
			ContainerEnv: map[string]any{"SPEC_ONLY": "late"},
		},
		BaseImage:             "alpine",
		ImageUser:             "image-user",
		LocalContextName:      "context",
		FeaturesContextSubdir: "_features",
		Features:              []features.Resolved{f},
	}
}

var testFeature = features.Resolved{
	Source:  "registry.example.test/collection/alpha:1",
	SafeID:  "abc-alpha",
	Options: map[string]any{"my-option.1": "two words"},
	Metadata: features.Metadata{
		ID:           "alpha",
		Version:      "1.0.0",
		Entrypoint:   "./entrypoint.sh",
		ContainerEnv: map[string]string{"ALPHA_HOME": "/opt/alpha"},
		Options:      map[string]features.Option{"level": {Type: "string", Default: "high"}},
	},
}

func TestBuild_FeatureInstallRunsWrapperWithEnvFiles(t *testing.T) {
	g := decodeGraph(t, featureGraphInputs(testFeature))
	dest := features.InstallDir + "/abc-alpha"
	exec := g.execWithArg(t, features.InstallCommand(dest))

	if user := exec.GetExec().Meta.User; user != "root" {
		t.Errorf("install user = %q, want root", user)
	}
	files := g.mkfiles(t, exec, dest)
	if got, want := files["/"+features.EnvFile], testFeature.EnvFileContent(); got != want {
		t.Errorf("env file = %q, want %q", got, want)
	}
	if !strings.Contains(files["/"+features.EnvFile], `MY_OPTION_1="two words"`) {
		t.Errorf("env file missing sanitized option: %q", files["/"+features.EnvFile])
	}
	if got, want := files["/"+features.InstallWrapperFile], testFeature.InstallWrapperContent(); got != want {
		t.Errorf("install wrapper = %q, want %q", got, want)
	}
	for _, arg := range exec.GetExec().Meta.Args {
		if strings.Contains(arg, "entrypoint.sh") {
			t.Errorf("feature entrypoint run at install time: %q", arg)
		}
	}
}

func TestBuild_FeatureContainerEnvPrecedesInstallAndSpecEnvFollows(t *testing.T) {
	g := decodeGraph(t, featureGraphInputs(testFeature))
	exec := g.execWithArg(t, features.InstallCommand(features.InstallDir+"/abc-alpha"))
	env := strings.Join(exec.GetExec().Meta.Env, "\n")
	if !strings.Contains(env, "ALPHA_HOME=/opt/alpha") {
		t.Errorf("feature containerEnv not visible to install: %v", exec.GetExec().Meta.Env)
	}
	if strings.Contains(env, "SPEC_ONLY") {
		t.Errorf("spec containerEnv must follow feature installs: %v", exec.GetExec().Meta.Env)
	}
}

func TestBuild_BuiltinEnvUsesConfiguredAndImageUsers(t *testing.T) {
	cases := []struct {
		name          string
		containerUser string
		imageUser     string
		wantContainer string
	}{
		{"image user", "", "image-user", "image-user"},
		{"spec containerUser wins", "spec-user", "image-user", "spec-user"},
		{"unknown image user is root", "", "", "root"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := featureGraphInputs(testFeature)
			in.Spec.ContainerUser = c.containerUser
			in.ImageUser = c.imageUser
			g := decodeGraph(t, in)
			want := features.BuiltinEnvContent(c.wantContainer, "remote-user")
			if got := g.allMkfiles()["/"+features.BuiltinEnvFile]; got != want {
				t.Fatalf("builtin env = %q, want %q", got, want)
			}
			path := features.InstallDir + "/" + features.BuiltinEnvFile
			g.execWithArg(t, features.BuiltinHomesCommand(path, c.wantContainer, "remote-user"))
		})
	}
}

func TestImageUser(t *testing.T) {
	if got := imageUser([]byte(`{"config":{"User":"image-user"}}`)); got != "image-user" {
		t.Errorf("imageUser = %q", got)
	}
	if got := imageUser(nil); got != "" {
		t.Errorf("imageUser(nil) = %q", got)
	}
}

func TestBuild_WorkspaceFolderIsCreated(t *testing.T) {
	got := marshalGraph(t, GraphInputs{
		Spec: &spec.DevContainer{
			Image:           "alpine",
			WorkspaceFolder: "/workspaces/demo",
		},
		BaseImage:          "alpine",
		ResolveImageConfig: false,
	})
	if !strings.Contains(got, "mkdir -p /workspaces/demo") {
		t.Errorf("missing workspace mkdir in graph:\n%s", got)
	}
}

func TestBuild_VSCodeServer(t *testing.T) {
	got := marshalGraph(t, GraphInputs{
		Spec:               &spec.DevContainer{Image: "alpine", RemoteUser: "vscode"},
		BaseImage:          "alpine",
		VSCodeServer:       &vscode.Result{Commit: "deadbeef", Quality: vscode.QualityStable, URL: "https://example.invalid/x.tar.gz", SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		ResolveImageConfig: false,
	})
	for _, want := range []string{
		"/home/vscode/.vscode-server/bin/deadbeef",
		"vscode-server.tar.gz",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in graph", want)
		}
	}
}

func TestBuild_RequiresBaseImage(t *testing.T) {
	_, err := Build(GraphInputs{Spec: &spec.DevContainer{}})
	if err == nil {
		t.Fatal("expected error for missing base image")
	}
}

// TestBuild_PlatformConstraint asserts that an explicit Platform is
// propagated to every authored vertex. Without the constraint, BuildKit
// builds for the daemon-default platform — which silently breaks
// cross-arch builds since features (which run via RUN) end up executing
// against the wrong arch.
func TestBuild_PlatformConstraint(t *testing.T) {
	state, err := Build(GraphInputs{
		Spec:                  &spec.DevContainer{Image: "alpine"},
		BaseImage:             "alpine",
		LocalContextName:      "context",
		FeaturesContextSubdir: "_features",
		Features: []features.Resolved{{
			Source:   "ghcr.io/devcontainers/features/node:1",
			SafeID:   "abc-node",
			Metadata: features.Metadata{ID: "node", Version: "1.0.0"},
		}},
		Platform: platform.Platform{OS: "linux", Arch: "arm64"},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	def, err := state.Marshal(context.Background())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var arm64Vertices int
	for _, dt := range def.Def {
		if strings.Contains(string(dt), "arm64") {
			arm64Vertices++
		}
	}
	if arm64Vertices == 0 {
		t.Fatalf("expected the arm64 platform to appear in at least one Op, got none")
	}
}

// TestBuild_BaseState verifies that supplying a BaseState llb.State
// short-circuits the llb.Image FROM. This is the path the gateway
// solver uses to chain on top of an upstream Dockerfile build.
func TestBuild_BaseState(t *testing.T) {
	base := llb.Scratch().File(llb.Mkdir("/marker", 0o755))
	state, err := Build(GraphInputs{
		Spec:      &spec.DevContainer{Image: ""},
		BaseState: &base,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := state.Marshal(context.Background()); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
}
