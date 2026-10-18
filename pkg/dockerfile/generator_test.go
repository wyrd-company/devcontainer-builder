// relationships:
//   references: [generator, metadata]

package dockerfile

import (
	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/vscode"
)

func TestGenerate_BareImage(t *testing.T) {
	out, err := Generate(Inputs{
		Spec: &spec.DevContainer{
			Image:           "ubuntu:22.04",
			RemoteUser:      "vscode",
			WorkspaceFolder: "/workspaces/demo",
		},
		BaseImage: "ubuntu:22.04",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		"FROM ubuntu:22.04",
		"USER vscode",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "WORKDIR") {
		t.Errorf("workspaceFolder became WORKDIR in:\n%s", got)
	}
}

func TestGenerate_DoesNotBakeRemoteEnv(t *testing.T) {
	out, err := Generate(Inputs{
		Spec: &spec.DevContainer{
			Image:           "alpine",
			ContainerEnv:    map[string]any{"CONTAINER_ONLY": "yes"},
			UpdateRemoteEnv: map[string]any{"REMOTE_ONLY": "secret"},
		},
		BaseImage: "alpine",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "ENV CONTAINER_ONLY=yes") {
		t.Fatalf("containerEnv missing from Dockerfile:\n%s", got)
	}
	if strings.Contains(got, "ENV REMOTE_ONLY=") {
		t.Fatalf("remoteEnv should not be emitted as image ENV:\n%s", got)
	}
}

func TestGenerate_VSCodeServer_AsRoot(t *testing.T) {
	out, err := Generate(Inputs{
		Spec:                    &spec.DevContainer{Image: "alpine"},
		BaseImage:               "alpine",
		VSCodeServer:            &vscode.Result{Commit: "abc123", Quality: vscode.QualityStable, URL: "u", SHA256: "deadbeef", Bytes: 1},
		VSCodeServerContextPath: "_vscode/server.tar.gz",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "/root/.vscode-server/bin/abc123") {
		t.Errorf("expected canonical install dir for root, got:\n%s", got)
	}
}

func TestGenerate_VSCodeServer_AsUser_ChownsHome(t *testing.T) {
	out, err := Generate(Inputs{
		Spec:                    &spec.DevContainer{Image: "alpine", RemoteUser: "vscode"},
		BaseImage:               "alpine",
		VSCodeServer:            &vscode.Result{Commit: "abc123", Quality: vscode.QualityStable, URL: "u", SHA256: "deadbeef", Bytes: 1},
		VSCodeServerContextPath: "_vscode/server.tar.gz",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "/home/vscode/.vscode-server/bin/abc123") {
		t.Errorf("expected vscode user install dir, got:\n%s", got)
	}
	if !strings.Contains(got, "chown -R") {
		t.Errorf("expected chown step, got:\n%s", got)
	}
}

// runFeatureSteps executes the feature-install steps of a generated
// Dockerfile on the host: ENV, COPY from the build context, COPY heredocs
// and RUN. Paths under features.InstallDir are redirected into root.
func runFeatureSteps(t *testing.T, dockerfile, contextDir, root string) {
	t.Helper()
	redirect := func(s string) string { return strings.ReplaceAll(s, features.InstallDir, root+features.InstallDir) }
	var exports []string
	lines := strings.Split(dockerfile, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "ENV "):
			exports = append(exports, "export "+strings.TrimPrefix(line, "ENV "))
		case strings.HasPrefix(line, "COPY ") && strings.Contains(line, "<<"):
			fields := strings.Fields(line)
			delim := strings.Trim(strings.TrimPrefix(fields[len(fields)-2], "<<"), `"`)
			dest := redirect(fields[len(fields)-1])
			var body []string
			for i++; lines[i] != delim; i++ {
				body = append(body, lines[i])
			}
			mode := os.FileMode(0o644)
			if strings.Contains(line, "--chmod=755") {
				mode = 0o755
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dest, []byte(strings.Join(body, "\n")+"\n"), mode); err != nil {
				t.Fatal(err)
			}
		case strings.HasPrefix(line, "COPY "):
			fields := strings.Fields(line)
			src, dest := filepath.Join(contextDir, fields[len(fields)-2]), redirect(fields[len(fields)-1])
			if out, err := exec.Command("cp", "-r", src, dest).CombinedOutput(); err != nil {
				t.Fatalf("cp %s %s: %v\n%s", src, dest, err, out)
			}
		case strings.HasPrefix(line, "RUN "):
			script := strings.Join(append(exports, redirect(strings.TrimPrefix(line, "RUN "))), "\n")
			cmd := exec.Command("/bin/sh", "-c", script)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("RUN %s: %v\n%s", line, err, out)
			}
		}
	}
}

func TestGenerate_FeatureInstallSeesOptionsAndBuiltins(t *testing.T) {
	contextDir, root := t.TempDir(), t.TempDir()
	envOut := filepath.Join(root, "install-env.txt")
	featDir := filepath.Join(contextDir, "_features", "abc-alpha")
	if err := os.MkdirAll(featDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"install.sh":    "#!/bin/sh\nenv > '" + envOut + "'\n",
		"entrypoint.sh": "#!/bin/sh\ntouch '" + filepath.Join(root, "entrypoint-ran") + "'\n",
	} {
		if err := os.WriteFile(filepath.Join(featDir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	feat := features.Resolved{
		Source:  "registry.example.test/collection/alpha:1",
		SafeID:  "abc-alpha",
		Options: map[string]any{"my-option.1": "two words"},
		Metadata: features.Metadata{
			ID:           "alpha",
			Entrypoint:   "./entrypoint.sh",
			ContainerEnv: map[string]string{"ALPHA_HOME": "/opt/alpha"},
			Options: map[string]features.Option{
				"my-option.1": {Type: "string", Default: "one"},
				"level":       {Type: "string", Default: "high"},
			},
		},
	}
	out, err := Generate(Inputs{
		Spec:               &spec.DevContainer{Image: "alpine", RemoteUser: "nobody-such-user", ContainerEnv: map[string]any{"SPEC_ONLY": "late"}},
		BaseImage:          "alpine",
		ImageUser:          "root",
		Features:           []features.Resolved{feat},
		FeaturesContextDir: "_features",
	})
	if err != nil {
		t.Fatal(err)
	}
	runFeatureSteps(t, string(out), contextDir, root)

	data, err := os.ReadFile(envOut)
	if err != nil {
		t.Fatalf("install.sh did not run: %v\n%s", err, out)
	}
	env := string(data)
	for _, want := range []string{
		"MY_OPTION_1=two words\n",
		"LEVEL=high\n",
		"ALPHA_HOME=/opt/alpha\n",
		"_CONTAINER_USER=root\n",
		"_REMOTE_USER=nobody-such-user\n",
		"_CONTAINER_USER_HOME=/root\n",
		"_REMOTE_USER_HOME=\n",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("install.sh env missing %q:\n%s", want, env)
		}
	}
	if strings.Contains(env, "SPEC_ONLY") {
		t.Errorf("spec containerEnv must follow feature installs:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "entrypoint-ran")); err == nil {
		t.Errorf("feature entrypoint ran at install time:\n%s", out)
	}
}

func TestGenerate_HeredocDelimiterAvoidsContent(t *testing.T) {
	var b strings.Builder
	emitHeredocCopy(&b, "", "/dest", "DEVCONTAINER_EOF\nDEVCONTAINER_EOF_1\n")
	got := b.String()
	if !strings.HasPrefix(got, `COPY <<"DEVCONTAINER_EOF_2" /dest`) || !strings.HasSuffix(got, "\nDEVCONTAINER_EOF_2\n") {
		t.Fatalf("heredoc = %q", got)
	}
}

func TestGenerate_MetadataIncludesBaseFeaturesAndConfig(t *testing.T) {
	out, err := Generate(Inputs{Spec: &spec.DevContainer{RemoteUser: "sample", PostCreate: spec.Command{Shell: `printf '%s' "$VALUE"`}}, BaseImage: "alpine", BaseMetadata: []metadata.Entry{{ID: "base"}}, Features: []features.Resolved{{Source: "./feature", Metadata: features.Metadata{ID: "feature", CapAdd: []string{"SYS_PTRACE"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	// Parse the emitted Dockerfile with BuildKit's own parser, then use its LABEL
	// expansion rules to recover the exact image label, including $ and quotes.
	parsed, err := parser.Parse(strings.NewReader(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	var label string
	for _, node := range parsed.AST.Children {
		if strings.EqualFold(node.Value, "label") {
			instruction, err := instructions.ParseInstruction(node)
			if err != nil {
				t.Fatal(err)
			}
			command := instruction.(*instructions.LabelCommand)
			if err := command.Expand(func(value string) (string, error) {
				word, _, err := shell.NewLex('\\').ProcessWord(value, shell.EnvsFromSlice(nil))
				return word, err
			}); err != nil {
				t.Fatal(err)
			}
			for _, kv := range command.Labels {
				if kv.Key == metadata.Label {
					label = kv.Value
				}
			}
		}
	}
	entries, err := metadata.Parse(label)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].ID != "base" || entries[1].ID != "./feature" || entries[2].PostCreate.Shell != `printf '%s' "$VALUE"` {
		t.Fatalf("label=%s entries=%+v", label, entries)
	}
}
