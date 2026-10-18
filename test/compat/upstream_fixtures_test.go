// relationships:
//
//	references: [taskfile]
package compat_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/wyrd-company/devcontainer-builder/pkg/builder"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	kubeprojection "github.com/wyrd-company/devcontainer-builder/pkg/projection/kubernetes"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestSupportedFieldsRemainInReferenceSchema(t *testing.T) {
	root := referenceRoot(t, "spec")
	body, err := os.ReadFile(filepath.Join(root, "reference", "spec", "schemas", "devContainer.base.schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema any
	if err := json.Unmarshal(body, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	properties := map[string]bool{}
	collectSchemaProperties(schema, properties)

	for _, name := range []string{
		"name",
		"image",
		"dockerFile",
		"build",
		"dockerComposeFile",
		"service",
		"runServices",
		"shutdownAction",
		"overrideCommand",
		"features",
		"overrideFeatureInstallOrder",
		"remoteUser",
		"containerUser",
		"remoteEnv",
		"containerEnv",
		"workspaceFolder",
		"workspaceMount",
		"mounts",
		"forwardPorts",
		"appPort",
		"updateRemoteUserUID",
		"onCreateCommand",
		"updateContentCommand",
		"postCreateCommand",
		"postStartCommand",
		"postAttachCommand",
		"init",
		"privileged",
		"capAdd",
		"securityOpt",
		"runArgs",
		"customizations",
		"dockerfile",
		"context",
		"args",
		"target",
		"cacheFrom",
		"options",
	} {
		if !properties[name] {
			t.Errorf("reference schema no longer exposes %q", name)
		}
	}
}

func TestCuratedCLIConfigFixtures(t *testing.T) {
	root := referenceRoot(t, "cli")

	t.Run("image spec maps runtime fields", func(t *testing.T) {
		path := filepath.Join(root, "reference", "cli", "src", "test", "configs", "image", ".devcontainer.json")
		dc, err := spec.Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if dc.Mode() != spec.ModeSingle {
			t.Fatalf("Mode = %v, want single", dc.Mode())
		}
		if dc.Image != "ubuntu:latest" {
			t.Errorf("Image = %q, want ubuntu:latest", dc.Image)
		}
		if got := dc.UpdateRemoteEnv["TEST_ESCAPING"]; got != "{\n  \"fo$o\": \"ba'r\"\n}" {
			t.Errorf("remoteEnv escaping = %#v", got)
		}
		if got, want := hookNames(lifecycle.Plan(dc, nil)), []string{"postCreateCommand"}; !equalStrings(got, want) {
			t.Fatalf("hooks = %v, want %v", got, want)
		}

		out, err := kubeprojection.Project(kubeprojection.ProjectRequest{
			Spec:  dc,
			Image: dc.Image,
			Name:  "compat-image",
		})
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		env := envMap(out.Pod.Spec.Containers[0].Env)
		if env["TEST_CE"] != "TEST_VALUE3" {
			t.Errorf("runArgs env TEST_CE = %q, want TEST_VALUE3", env["TEST_CE"])
		}
	})

	t.Run("parallel lifecycle commands stay grouped by hook", func(t *testing.T) {
		path := filepath.Join(root, "reference", "cli", "src", "test", "configs", "dockerfile-with-parallel-commands", ".devcontainer.json")
		dc, err := spec.Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if dc.Build == nil || dc.Build.Dockerfile != "Dockerfile" {
			t.Fatalf("Build = %+v, want Dockerfile", dc.Build)
		}
		if got, want := hookNames(lifecycle.Plan(dc, nil)), []string{"postCreateCommand", "postStartCommand", "postAttachCommand"}; !equalStrings(got, want) {
			t.Fatalf("hooks = %v, want %v", got, want)
		}
		if len(dc.PostCreate.Parallel) != 2 || len(dc.PostStart.Parallel) != 2 || len(dc.PostAttach.Parallel) != 2 {
			t.Fatalf("parallel command groups = create:%d start:%d attach:%d, want 2 each",
				len(dc.PostCreate.Parallel), len(dc.PostStart.Parallel), len(dc.PostAttach.Parallel))
		}
	})

	t.Run("compose fixture loads and projects primary service", func(t *testing.T) {
		workspace := filepath.Join(root, "reference", "cli", "src", "test", "configs", "compose-image-without-features-minimal")
		dc, err := spec.LoadFromWorkspace(workspace)
		if err != nil {
			t.Fatalf("LoadFromWorkspace: %v", err)
		}
		if dc.Mode() != spec.ModeCompose {
			t.Fatalf("Mode = %v, want compose", dc.Mode())
		}

		project, err := dockercompose.LoadProject(context.Background(), dockercompose.LoadInputs{
			Spec:      dc,
			Workspace: workspace,
			LocalEnv:  map[string]string{},
		})
		if err != nil {
			t.Fatalf("LoadProject: %v", err)
		}
		app, err := project.GetService("app")
		if err != nil {
			t.Fatalf("GetService: %v", err)
		}
		if app.Image != "ubuntu:latest" {
			t.Fatalf("app image = %q, want ubuntu:latest", app.Image)
		}

		out, err := kubeprojection.ProjectCompose(kubeprojection.ComposeProjectRequest{
			Spec:           dc,
			SpecPath:       dc.Path,
			LocalWorkspace: workspace,
			Project:        project,
			Name:           "compat-compose",
		})
		if err != nil {
			t.Fatalf("ProjectCompose: %v", err)
		}
		if len(out.Pod.Spec.Containers) != 1 {
			t.Fatalf("containers = %d, want 1", len(out.Pod.Spec.Containers))
		}
		primary := out.Pod.Spec.Containers[0]
		if primary.Name != "app" || primary.Image != "ubuntu:latest" {
			t.Fatalf("primary = %s/%s, want app/ubuntu:latest", primary.Name, primary.Image)
		}
		if !hasMount(primary.VolumeMounts, "/workspace") {
			t.Fatalf("primary mounts = %+v, want /workspace", primary.VolumeMounts)
		}
		if out.Pod.Annotations[kubeprojection.AnnotationComposeService] != "app" {
			t.Errorf("compose service annotation = %q", out.Pod.Annotations[kubeprojection.AnnotationComposeService])
		}
	})
}

func TestCuratedCLIFixtureGeneratesWrapperDockerfile(t *testing.T) {
	root := referenceRoot(t, "cli")
	workspace := filepath.Join(root, "reference", "cli", "src", "test", "configs", "dockerfile-without-features")

	result, err := builder.New().Generate(context.Background(), builder.Options{
		Workspace:        workspace,
		WorkDir:          t.TempDir(),
		SkipFeatures:     true,
		SkipVSCodeServer: true,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	body, err := os.ReadFile(result.FinalDockerfile)
	if err != nil {
		t.Fatalf("read generated Dockerfile: %v", err)
	}
	got := string(body)
	for _, want := range []string{
		"# syntax=docker/dockerfile:1.7",
		"FROM <unresolved-base-build-required>",
		"USER root",
		"SHELL [\"/bin/sh\", \"-c\"]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated Dockerfile missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "TEST_ESCAPING") {
		t.Fatalf("remoteEnv leaked into generated Dockerfile:\n%s", got)
	}
}

func TestCuratedCLILocalFeaturesGenerateInstallSteps(t *testing.T) {
	root := referenceRoot(t, "cli")
	workspace := filepath.Join(root, "reference", "cli", "src", "test", "container-features", "configs", "dockerfile-with-v2-local-features-config-inside-dev-container-folder")

	result, err := builder.New().Generate(context.Background(), builder.Options{
		Workspace:        workspace,
		WorkDir:          t.TempDir(),
		SkipVSCodeServer: true,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	gotIDs := make([]string, 0, len(result.Features))
	for _, f := range result.Features {
		gotIDs = append(gotIDs, f.Metadata.ID)
	}
	if want := []string{"localFeatureA", "localFeatureB"}; !equalStrings(gotIDs, want) {
		t.Fatalf("feature order = %v, want %v", gotIDs, want)
	}

	body, err := os.ReadFile(result.FinalDockerfile)
	if err != nil {
		t.Fatalf("read generated Dockerfile: %v", err)
	}
	got := string(body)
	for _, want := range []string{
		"# Feature: localFeatureA (./localFeatureA)",
		"GREETING='Hello there'",
		"PUNCTUATION='!!!!'",
		"# Feature: localFeatureB (./localFeatureB)",
		"FAVORITE=gold",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated Dockerfile missing %q:\n%s", want, got)
		}
	}
}

func TestCuratedCLILocalFeatureInstallOrder(t *testing.T) {
	root := referenceRoot(t, "cli")
	workspace := filepath.Join(root, "reference", "cli", "src", "test", "container-features", "configs", "feature-dependencies", "installsAfter", "local-simple")

	result, err := builder.New().Generate(context.Background(), builder.Options{
		Workspace:        workspace,
		WorkDir:          t.TempDir(),
		SkipVSCodeServer: true,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	got := make([]string, 0, len(result.Features))
	for _, f := range result.Features {
		got = append(got, f.Metadata.ID)
	}
	if want := []string{"c", "a"}; !equalStrings(got, want) {
		t.Fatalf("feature order = %v, want %v", got, want)
	}
}

func TestCuratedCLIUnsupportedBuildOptionsError(t *testing.T) {
	root := referenceRoot(t, "cli")
	workspace := filepath.Join(root, "reference", "cli", "src", "test", "configs", "dockerfile-with-target")

	_, err := builder.New().Build(context.Background(), builder.Options{
		Engine:           builder.EngineBuildKit,
		Workspace:        workspace,
		WorkDir:          t.TempDir(),
		SkipFeatures:     true,
		SkipVSCodeServer: true,
	})
	if err == nil {
		t.Fatal("expected unsupported build options error")
	}
	if !strings.Contains(err.Error(), "build.options is not supported by the buildkit engine") {
		t.Fatalf("error = %q, want stable build.options message", err.Error())
	}
}

func collectSchemaProperties(v any, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		if props, ok := x["properties"].(map[string]any); ok {
			for name, child := range props {
				out[name] = true
				collectSchemaProperties(child, out)
			}
		}
		for _, child := range x {
			collectSchemaProperties(child, out)
		}
	case []any:
		for _, child := range x {
			collectSchemaProperties(child, out)
		}
	}
}

func referenceRoot(t *testing.T, name string) string {
	t.Helper()
	root := repoRoot(t)
	if _, err := os.Stat(filepath.Join(root, "reference", name, ".git")); os.IsNotExist(err) {
		t.Skipf("reference/%s is not initialized; run task reference:init", name)
	} else if err != nil {
		t.Fatalf("inspect reference/%s: %v", name, err)
	}
	return root
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if body, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil &&
			strings.Contains(string(body), "module github.com/wyrd-company/devcontainer-builder") {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			t.Fatal("repository root not found")
		}
		dir = next
	}
}

func hookNames(plan []lifecycle.Phase) []string {
	out := make([]string, 0, len(plan))
	for _, p := range plan {
		out = append(out, string(p.Hook))
	}
	return out
}

func envMap(env []corev1.EnvVar) map[string]string {
	out := map[string]string{}
	for _, e := range env {
		out[e.Name] = e.Value
	}
	return out
}

func hasMount(mounts []corev1.VolumeMount, target string) bool {
	for _, m := range mounts {
		if m.MountPath == target {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
