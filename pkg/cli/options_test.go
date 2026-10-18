// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/builder"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

func TestSkipLifecycle(t *testing.T) {
	dc := &spec.DevContainer{OnCreate: spec.Command{Shell: "create"}, UpdateContent: spec.Command{Shell: "update"}, PostCreate: spec.Command{Shell: "post-create"}, PostStart: spec.Command{Shell: "start"}, PostAttach: spec.Command{Shell: "attach"}}
	resolved := []features.Resolved{{Metadata: features.Metadata{OnCreateCommand: spec.Command{Shell: "feature-create"}, UpdateContent: spec.Command{Shell: "feature-update"}, PostCreateCommand: spec.Command{Shell: "feature-post-create"}, PostStartCommand: spec.Command{Shell: "feature-start"}, PostAttachCommand: spec.Command{Shell: "feature-attach"}}}}
	for _, tc := range []struct {
		name    string
		options skipOptions
		want    int
	}{
		{"none", skipOptions{}, 10}, {"all", skipOptions{postCreate: true}, 0}, {"attach", skipOptions{postAttach: true}, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filtered, feats := tc.options.apply(dc, resolved)
			plan := lifecycle.Plan(filtered, feats)
			if len(plan) != tc.want {
				t.Fatalf("plan has %d phases, want %d: %v", len(plan), tc.want, plan)
			}
			for _, phase := range plan {
				if tc.options.postAttach && phase.Hook == lifecycle.HookPostAttach {
					t.Fatal("postAttach was not skipped")
				}
			}
			if len(lifecycle.Plan(dc, resolved)) != 10 {
				t.Fatal("filter mutated caller configuration or metadata")
			}
		})
	}
}

func TestBuildOptions(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "devcontainer.json")
	if err := os.WriteFile(cfg, []byte(`{"image":"alpine:3.20"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"push-load", []string{"--push", "--load"}, 2},
		{"invalid-arg", []string{"--build-arg", "invalid"}, 2},
		{"empty-label-key", []string{"--label", "=value"}, 2},
		{"up-export", []string{"--output", t.TempDir()}, 2},
		{"invalid-uid", []string{"--update-remote-user-uid-default", "invalid"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"up", "--config", cfg}
			args = append(args, tc.args...)
			invoke(t, tc.code, args...)
		})
	}
	a := &application{logs: &logWriter{dst: io.Discard}}
	o := buildOptions{buildArgs: []string{"KEY=one=two"}, labels: []string{"example.label=value"}}
	dc, err := spec.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	result, err := o.prepare(a, dc, "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Load || result.BuildArgs["KEY"] != "one=two" || result.Labels["example.label"] != "value" || len(result.Tags) != 1 || result.Tags[0] == defaultImage("/other/workspace") {
		t.Fatalf("build options=%+v", result)
	}
	o.options.Push = true
	result, err = o.prepare(a, dc, "/workspace")
	if err != nil || result.Load {
		t.Fatalf("push options=%+v error=%v", result, err)
	}
}

func TestCommandSubstitution(t *testing.T) {
	var command spec.Command
	if err := json.Unmarshal([]byte(`{"one":"echo ${localEnv:VALUE}","two":["echo","${containerEnv:VALUE}"]}`), &command); err != nil {
		t.Fatal(err)
	}
	result := resolveCommand(command, substitute.Context{LocalEnv: map[string]string{"VALUE": "local"}}, map[string]string{"VALUE": "container"})
	if result.Parallel["one"].Shell != "echo local" || result.Parallel["two"].Args[1] != "container" {
		t.Fatalf("command=%+v", result)
	}
	if command.Parallel["two"].Args[1] != "${containerEnv:VALUE}" {
		t.Fatal("mutated caller command")
	}
}

func TestExecArgumentBoundary(t *testing.T) {
	// The parser is the real Cobra tree. Replace only the transport action so
	// this assertion can observe argv without starting Docker.
	a := &application{result: map[string]any{}, stderr: io.Discard}
	root := a.command()
	cmd, _, err := root.Find([]string{"exec"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	cmd.RunE = func(_cmd *cobra.Command, args []string) error { got = append([]string(nil), args...); return nil }
	root.SetArgs([]string{"exec", "printf", "--workspace-folder", "/example", "--", "--log-format", "literal"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "printf" || got[1] != "--log-format" || got[2] != "literal" {
		t.Fatalf("argv=%q", got)
	}
	if value, _ := cmd.Flags().GetString("workspace-folder"); value != "/example" {
		t.Fatalf("workspace flag=%q", value)
	}
}

func TestOptionalConfiguration(t *testing.T) {
	workspace := t.TempDir()
	dc, got, err := (configOptions{workspace: workspace}).optionalLoad()
	if err != nil || dc.Path != "" || got != workspace {
		t.Fatalf("optional config: %+v %q %v", dc, got, err)
	}
	if _, _, err := (configOptions{workspace: workspace, path: filepath.Join(workspace, "missing")}).optionalLoad(); err == nil {
		t.Fatal("explicit missing config was ignored")
	}
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(`{invalid`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (configOptions{workspace: workspace}).optionalLoad(); err == nil {
		t.Fatal("malformed config was ignored")
	}
}

func TestImageUpPreservesBuildCustomization(t *testing.T) {
	dc := &spec.DevContainer{Image: "alpine:3.20"}
	if needsBuild(dc, builder.Options{}) {
		t.Fatal("plain image should use host pulling")
	}
	for _, tc := range []struct {
		name    string
		options builder.Options
	}{
		{"platform", builder.Options{Platforms: []string{"linux/arm64"}}},
		{"labels", builder.Options{Labels: map[string]string{"example": "value"}}},
		{"pull", builder.Options{Pull: true}},
		{"no-cache", builder.Options{NoCache: true}},
		{"cache-from", builder.Options{CacheFrom: []string{"type=registry,ref=example:cache"}}},
		{"cache-to", builder.Options{CacheTo: []string{"type=inline"}}},
		{"server", builder.Options{InstallVSCodeServer: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !needsBuild(dc, tc.options) {
				t.Fatalf("%s was discarded", tc.name)
			}
		})
	}
}
