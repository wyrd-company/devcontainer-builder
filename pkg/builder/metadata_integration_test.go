//go:build docker_integration

// relationships:
//
//	references: [builder, metadata, generator, up]
package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	imagetypes "github.com/docker/docker/api/types/image"

	"github.com/wyrd-company/devcontainer-builder/pkg/buildkit"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	dockerhost "github.com/wyrd-company/devcontainer-builder/pkg/host/docker"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// TestMetadataPrebuiltIntegration exercises the label from each writer through
// image inspection into Up with no configured features, then into a reused Up.
func TestMetadataPrebuiltIntegration(t *testing.T) {
	dockerConfig := t.TempDir()
	if err := os.WriteFile(filepath.Join(dockerConfig, "config.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_CONFIG", dockerConfig)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cli := requireEngine(t, ctx)
	for _, mode := range []string{"image", "upstream-dockerfile", "generated-dockerfile"} {
		t.Run(mode, func(t *testing.T) {
			workspace := t.TempDir()
			feature := filepath.Join(workspace, "feature")
			if err := os.MkdirAll(feature, 0755); err != nil {
				t.Fatal(err)
			}
			files := map[string]string{
				filepath.Join(feature, "devcontainer-feature.json"): `{"id":"sample","version":"1.0.0","capAdd":["SYS_PTRACE"],"init":true,"securityOpt":["no-new-privileges"],"mounts":[{"type":"volume","target":"/sample-cache"}],"containerEnv":{"FEATURE_ENV":"feature"},"customizations":{"editor":{"sample":true}},"entrypoint":"echo entrypoint > /tmp/sample-entrypoint","postCreateCommand":"printf '%s\\n' '${localEnv:SAMPLE_FEATURE}' >> /tmp/sample-hooks"}`,
				filepath.Join(feature, "install.sh"):                "#!/bin/sh\nset -eu\necho installed > /tmp/sample-install\nadduser -D sample-user\n",
			}
			for path, content := range files {
				if err := os.WriteFile(path, []byte(content), 0755); err != nil {
					t.Fatal(err)
				}
			}
			dc := map[string]any{"image": "alpine:3.20", "features": map[string]any{"./feature": map[string]any{}}, "remoteUser": "sample-user", "containerUser": "root", "containerEnv": map[string]any{"CONFIG_ENV": "config"}, "postCreateCommand": []string{"/bin/sh", "-c", `printf '%s\n' "$CONFIG_ENV" >> /tmp/sample-hooks; id -un > /tmp/sample-remote-user`}}
			if mode == "upstream-dockerfile" {
				delete(dc, "image")
				dc["build"] = map[string]any{"dockerfile": "Dockerfile", "context": "."}
				if err := os.WriteFile(filepath.Join(workspace, "Dockerfile"), []byte("FROM alpine:3.20\nLABEL devcontainer.metadata=\"{\\\"id\\\":\\\"base\\\",\\\"postCreateCommand\\\":\\\"printf 'base\\\\n' >> /tmp/sample-hooks\\\"}\"\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "generated-dockerfile" {
				baseTag := fmt.Sprintf("dcb-metadata-base:%d", time.Now().UnixNano())
				t.Cleanup(func() { _, _ = cli.ImageRemove(context.Background(), baseTag, imagetypes.RemoveOptions{Force: true}) })
				baseDir := t.TempDir()
				label, err := metadata.DockerfileLabel([]metadata.Entry{{ID: "base", PostCreate: spec.Command{Shell: "printf 'base\\n' >> /tmp/sample-hooks"}}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(baseDir, "Dockerfile"), []byte("FROM alpine:3.20\n"+label+"\n"), 0644); err != nil {
					t.Fatal(err)
				}
				if _, err := buildkit.SolveDockerfile(ctx, buildkit.DockerfileBuild{ContextDir: baseDir, DockerfileDir: baseDir, Filename: "Dockerfile"}, buildkit.SolveOptions{Docker: cli, Tags: []string{baseTag}, Load: true, Progress: "plain", Stderr: testLogWriter{t}}); err != nil {
					t.Fatal(err)
				}
				dc["image"] = baseTag
			}
			raw, err := json.Marshal(dc)
			if err != nil {
				t.Fatal(err)
			}
			specPath := filepath.Join(workspace, ".devcontainer.json")
			if err := os.WriteFile(specPath, raw, 0644); err != nil {
				t.Fatal(err)
			}
			tag := fmt.Sprintf("dcb-metadata-test-%s:%d", mode, time.Now().UnixNano())
			t.Cleanup(func() {
				if _, err := cli.ImageRemove(context.Background(), tag, imagetypes.RemoveOptions{Force: true}); err != nil {
					t.Logf("remove image: %v", err)
				}
			})
			opts := Options{Workspace: workspace, Tags: []string{tag}, Load: true, Docker: cli, SkipImageMetaResolve: mode == "generated-dockerfile", WorkDir: t.TempDir(), KeepWorkDir: true, Logger: t.Logf, Stdout: io.Discard, Stderr: testLogWriter{t}}
			var resolved []features.Resolved
			if mode == "generated-dockerfile" {
				result, err := New().Generate(ctx, opts)
				if err != nil {
					t.Fatal(err)
				}
				resolved = result.Features
				// Build the unchanged generated artifact with the daemon's bundled
				// Dockerfile frontend. cmdline prevents syntax-image forwarding, so
				// this metadata test needs no external frontend image/cache.
				if _, err := buildkit.SolveDockerfile(ctx, buildkit.DockerfileBuild{ContextDir: result.ContextDir, DockerfileDir: filepath.Dir(result.FinalDockerfile), Filename: filepath.Base(result.FinalDockerfile)}, buildkit.SolveOptions{Docker: cli, Tags: []string{tag}, Load: true, FrontendAttrs: map[string]string{"cmdline": ""}, Progress: "plain", Stderr: testLogWriter{t}}); err != nil {
					t.Fatal(err)
				}
			} else {
				built, err := New().Build(ctx, opts)
				if err != nil {
					t.Fatal(err)
				}
				resolved = built.Features
			}
			img, err := cli.ImageInspect(ctx, tag)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := metadata.Parse(img.Config.Labels[metadata.Label])
			if err != nil {
				t.Fatal(err)
			}
			wantEntries := 2
			if mode != "image" {
				wantEntries = 3
			}
			if len(entries) != wantEntries {
				t.Fatalf("metadata entries=%+v", entries)
			}
			merged := metadata.Merge(&spec.DevContainer{}, entries)
			if len(merged.CapAdd) != 1 || merged.CapAdd[0] != "SYS_PTRACE" || len(merged.PostCreateCommands) != wantEntries || len(merged.Customizations["editor"]) != 1 {
				t.Fatalf("label merge=%+v", merged)
			}
			h := dockerhost.New(cli, dockerhost.Options{Logger: t.Logf})
			for _, fromCurrentSpec := range []bool{false, true} {
				t.Run(fmt.Sprintf("built-current-spec-%t", fromCurrentSpec), func(t *testing.T) {
					req := dockerhost.UpRequest{Spec: &spec.DevContainer{Image: tag, WorkspaceMount: "type=volume,target=/workspace", WorkspaceFolder: "/workspace"}, Image: tag, LocalWorkspace: workspace, SpecPath: specPath}
					req.LocalEnv = map[string]string{"SAMPLE_FEATURE": "feature"}
					req.LocalWorkspace = fmt.Sprintf("%s-%t", workspace, fromCurrentSpec)
					if fromCurrentSpec {
						loaded, err := spec.Load(specPath)
						if err != nil {
							t.Fatal(err)
						}
						loaded.WorkspaceMount = "type=volume,target=/workspace"
						loaded.WorkspaceFolder = "/workspace"
						req.Spec, req.ImageBuilt, req.Features = loaded, true, resolved
					}
					result, err := h.Up(ctx, req)
					if result != nil && result.ContainerID != "" {
						t.Cleanup(func() {
							_ = cli.ContainerRemove(context.Background(), result.ContainerID, container.RemoveOptions{Force: true, RemoveVolumes: true})
						})
					}
					if err != nil {
						t.Fatal(err)
					}
					inspect, err := cli.ContainerInspect(ctx, result.ContainerID)
					if err != nil {
						t.Fatal(err)
					}
					if inspect.Config.User != "root" {
						t.Fatalf("containerUser=%q", inspect.Config.User)
					}
					caps := strings.Join(inspect.HostConfig.CapAdd, ",")
					if !strings.Contains(caps, "SYS_PTRACE") || inspect.HostConfig.Init == nil || !*inspect.HostConfig.Init || !strings.Contains(strings.Join(inspect.HostConfig.SecurityOpt, ","), "no-new-privileges") {
						t.Fatalf("runtime host config=%+v", inspect.HostConfig)
					}
					foundMount := false
					for _, mount := range inspect.Mounts {
						if mount.Destination == "/sample-cache" {
							foundMount = true
						}
					}
					if !foundMount {
						t.Fatalf("missing feature mount: %v", inspect.Mounts)
					}
					execute := func(cmd string) string {
						t.Helper()
						out, err := h.Exec(ctx, dockerhost.ExecRequest{ContainerID: result.ContainerID, User: "root", Cmd: []string{"/bin/sh", "-c", cmd}})
						if err != nil || out.ExitCode != 0 {
							t.Fatalf("exec %s=%+v %v", cmd, out, err)
						}
						return string(out.Stdout)
					}
					wantHooks := "feature\nconfig\n"
					if mode != "image" {
						wantHooks = "base\n" + wantHooks
					}
					if hooks := execute("cat /tmp/sample-hooks"); hooks != wantHooks {
						t.Fatalf("hook order=%q want %q", hooks, wantHooks)
					}
					if got := execute("cat /tmp/sample-entrypoint; printf '%s %s' \"$FEATURE_ENV\" \"$CONFIG_ENV\""); got != "entrypoint\nfeature config" {
						t.Fatalf("entrypoint/env=%q", got)
					}
					if got := execute("cat /tmp/sample-remote-user"); got != "sample-user\n" {
						t.Fatalf("remoteUser=%q", got)
					}
					status := execute("cat /proc/self/status")
					foundCapEff := false
					for _, line := range strings.Split(status, "\n") {
						if strings.HasPrefix(line, "CapEff:") {
							foundCapEff = true
							capEff, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "CapEff:")), 16, 64)
							if err != nil || capEff&(1<<19) == 0 {
								t.Fatalf("SYS_PTRACE absent from %q", line)
							}
						}
					}
					if !foundCapEff {
						t.Fatal("CapEff missing from process status")
					}
					// Reuse reads the container label and does not append lifecycle hooks again.
					req.Spec.PostAttach = spec.Command{Shell: "touch /tmp/sample-unexpected"}
					reused, err := h.Up(ctx, req)
					if err != nil {
						t.Fatal(err)
					}
					if reused.CreatedThisCall || reused.ContainerID != result.ContainerID || execute("cat /tmp/sample-hooks; test ! -e /tmp/sample-unexpected") != wantHooks {
						t.Fatalf("reuse=%+v", reused)
					}
				})
			}
		})
	}
}
