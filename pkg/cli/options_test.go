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

func TestBuildOptions(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	cfg := filepath.Join(t.TempDir(), "devcontainer.json")
	if err := os.WriteFile(cfg, []byte(`{"image":"alpine:3.20"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{

		{"invalid-arg", []string{"--build-arg", "invalid"}, 2},
		{"empty-label-key", []string{"--label", "=value"}, 2},

		{"invalid-uid", []string{"--update-remote-user-uid-default", "invalid"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"up", "--config", cfg}
			args = append(args, tc.args...)
			invoke(t, tc.code, args...)
		})
	}

	// Build retains its mutually exclusive export modes.
	conflict, _ := invoke(t, 2, "build", "--config", cfg, "--push", "--load")
	if conflict["message"] != "--push and --load are mutually exclusive" {
		t.Fatalf("push/load conflict: %v", conflict)
	}
	for _, mode := range []string{"--push", "--load"} {
		invoke(t, 2, "build", "--config", cfg, "--output", t.TempDir(), mode)
	}

	a := &application{logs: &logWriter{dst: io.Discard}}
	o := buildOptions{buildArgs: []string{"KEY=one=two"}, labels: []string{"example.label=value"}}
	dc, err := spec.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	result, err := o.prepare(a, dc, "/workspace", false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Load || result.BuildArgs["KEY"] != "one=two" || result.Labels["example.label"] != "value" || len(result.Tags) != 1 || result.Tags[0] == defaultImage("/other/workspace") {
		t.Fatalf("build options=%+v", result)
	}

	cacheOnly, err := o.prepare(a, dc, "/workspace", true)
	if err != nil || cacheOnly.Load {
		t.Fatalf("explicit --load=false was ignored: %+v %v", cacheOnly, err)
	}
	o.options.Push = true
	result, err = o.prepare(a, dc, "/workspace", false)
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

func TestUpFlagSurface(t *testing.T) {
	a := &application{}
	up, build := a.upCommand(), a.buildCommand(false)
	for _, name := range []string{"push", "load", "output", "no-cache"} {
		if up.Flags().Lookup(name) != nil {
			t.Errorf("up registers --%s", name)
		}
		if build.Flags().Lookup(name) == nil {
			t.Errorf("build missing --%s", name)
		}
		invoke(t, 2, "up", "--"+name+"=value")
	}
	for _, name := range []string{"build-no-cache", "workspace-folder", "image-name", "label"} {
		if up.Flags().Lookup(name) == nil {
			t.Errorf("up missing --%s", name)
		}
	}
}

func TestUIDModeMapping(t *testing.T) {
	for _, platform := range []string{"linux", "darwin", "windows"} {
		for _, mode := range []string{"default", "on", "off", "never", "invalid", ""} {
			t.Run(platform+"/"+mode, func(t *testing.T) {
				cmd := (&application{}).upCommand()
				if mode != "default" {
					if err := cmd.ParseFlags([]string{"--update-remote-user-uid-default=" + mode}); err != nil {
						t.Fatal(err)
					}
				}
				update, err := resolveUIDUpdate(cmd, platform)
				if mode == "invalid" || mode == "" {
					if err == nil {
						t.Fatal("invalid mode accepted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				want := platform == "linux" && mode != "never" || platform == "darwin" && mode == "on"
				if (update != nil) != want {
					t.Fatalf("UID request=%+v, want enabled=%v", update, want)
				}
				if update != nil && (update.DefaultOff != (mode == "off") || update.HostUID != os.Getuid() || update.HostGID != os.Getgid()) {
					t.Fatalf("unexpected UID request: %+v", update)
				}
			})
		}
	}
}

func TestLifecycleResultShape(t *testing.T) {
	phases := []lifecycle.Phase{{Hook: lifecycle.HookPostAttach, When: lifecycle.WhenAttach, Origin: lifecycle.Origin{Source: "devcontainer.json"}, Command: spec.Command{Shell: "echo example"}}}
	encoded, err := json.Marshal(map[string]any{"phasesRun": phaseResults(phases)})
	if err != nil || string(encoded) != `{"phasesRun":[{"hook":"postAttachCommand","origin":"devcontainer.json"}]}` {
		t.Fatalf("phases result=%s (%v)", encoded, err)
	}
	encoded, err = json.Marshal(phaseResults(nil))
	if err != nil || string(encoded) != "[]" {
		t.Fatalf("empty phases=%s (%v)", encoded, err)
	}
}

func TestComposeUpRequestMetadataMapping(t *testing.T) {
	t.Setenv("SAMPLE_VALUE", "local")
	dc := &spec.DevContainer{Path: "/workspace/devcontainer.json", Service: "app", PostCreate: spec.Command{Shell: "create"}, PostAttach: spec.Command{Shell: "attach"}}
	built := &builder.Result{Features: []features.Resolved{{Source: "./sample", Metadata: features.Metadata{PostCreateCommand: spec.Command{Shell: "feature"}}}}}
	for _, tc := range []struct {
		name  string
		built *builder.Result
		skip  skipOptions
	}{
		{"prebuilt", nil, skipOptions{}}, {"derived", built, skipOptions{}}, {"skip-all", built, skipOptions{postCreate: true}}, {"skip-attach", built, skipOptions{postAttach: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := composeUpRequest(dc, "/workspace", "sample:dev", tc.built, tc.skip)
			if req.Spec != dc || req.SpecPath != dc.Path || req.LocalWorkspace != "/workspace" || req.PrimaryImage != "sample:dev" || req.ImageBuilt != (tc.built != nil) || req.SkipLifecycle != tc.skip.postCreate || req.SkipPostAttach != tc.skip.postAttach || req.LocalEnv["SAMPLE_VALUE"] != "local" {
				t.Fatalf("request=%+v", req)
			}
			if tc.built != nil && (len(req.Features) != 1 || req.Features[0].Source != "./sample" || req.Features[0].Metadata.PostCreateCommand.Shell != "feature") {
				t.Fatalf("features=%+v", req.Features)
			}
			if dc.PostCreate.Shell != "create" || dc.PostAttach.Shell != "attach" {
				t.Fatal("skip removed raw config hooks")
			}
		})
	}
}
