//go:build docker_integration

// relationships:
//
//	references: [options_feature_controls, ../builder/builder, ../metadata/metadata]
package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	imagetypes "github.com/docker/docker/api/types/image"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
)

func featureControlFixture(t *testing.T, mode string, withFeature bool) string {
	t.Helper()
	workspace := t.TempDir()
	base := `"image":"alpine:3.20"`
	if strings.Contains(mode, "dockerfile") {
		base = `"build":{"dockerfile":"Dockerfile","context":"."}`
		// This invalid frontend reference fails immediately if the omit handler is lost.
		if err := os.WriteFile(filepath.Join(workspace, "Dockerfile"), []byte("# syntax=invalid[\nFROM alpine:3.20\nRUN printf built > /tmp/base-marker\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if strings.HasPrefix(mode, "compose") {
		id := sha256.Sum256([]byte(workspace))
		service := "    image: alpine:3.20\n"
		if strings.Contains(mode, "dockerfile") {
			service = "    image: " + strings.Replace(defaultImage(workspace), ":latest", ":base", 1) + "\n    build: .\n"
		}
		body := fmt.Sprintf("name: sample-%x\nservices:\n  app:\n%s", id[:6], service)
		if err := os.WriteFile(filepath.Join(workspace, "compose.yml"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		base = `"dockerComposeFile":"compose.yml","service":"app","workspaceFolder":"/"`
	}
	feature := ""
	if withFeature {
		feature = `,"features":{"./sample":{}}`
		dir := filepath.Join(workspace, "sample")
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string]string{"devcontainer-feature.json": `{"id":"sample","version":"1.0.0","customizations":{"editor":{"value":"feature"}}}`, "install.sh": "#!/bin/sh\nset -eu\nprintf installed > /tmp/feature-marker\n"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0755); err != nil {
				t.Fatal(err)
			}
		}
	}
	config := `{` + base + feature + `,"updateRemoteUserUID":false,"remoteEnv":{"VALUE":"configured"},"customizations":{"editor":{"value":"config"}},"postCreateCommand":"printf '%s' \"$VALUE\" > /tmp/remote-marker"}`
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func TestFeatureMetadataControlsDockerIntegration(t *testing.T) {
	t.Setenv("BUILDKIT_HOST", "")
	cli, err := newDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	ctx := context.Background()
	for _, mode := range []string{"image", "compose", "dockerfile", "compose-dockerfile"} {
		for _, withFeature := range []bool{false, true} {
			for _, omit := range []bool{false, true} {
				// Dockerfile cases prove syntax omission plus metadata propagation once.
				if strings.Contains(mode, "dockerfile") && (!omit || !withFeature) {
					continue
				}
				t.Run(fmt.Sprintf("%s/feature=%t/omit=%t", mode, withFeature, omit), func(t *testing.T) {
					workspace := featureControlFixture(t, mode, withFeature)
					args := []string{"up", "--workspace-folder", workspace}
					if withFeature {
						args = append(args, "--image-name", defaultImage(workspace))
					}
					if omit {
						args = append(args, "--omit-config-remote-env-from-metadata")
					}
					if strings.Contains(mode, "dockerfile") {
						args = append(args, "--omit-syntax-directive")
					}
					// Cleanup uses only this workspace and the derived tag this invocation owns.
					t.Cleanup(func() {
						invoke(t, 0, "down", "--workspace-folder", workspace)
						_, _ = cli.ImageRemove(ctx, defaultImage(workspace), imagetypes.RemoveOptions{Force: true})
						if mode == "compose-dockerfile" {
							_, _ = cli.ImageRemove(ctx, strings.Replace(defaultImage(workspace), ":latest", ":base", 1), imagetypes.RemoveOptions{Force: true})
						}
					})
					result, _ := invoke(t, 0, args...)
					id := result["containerId"].(string)
					info, err := cli.ContainerInspect(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
					entries, err := metadata.Parse(info.Config.Labels[metadata.Label])
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, e := range entries {
						if e.RemoteEnv["VALUE"] == "configured" {
							found = true
						}
					}
					if found == omit {
						t.Fatalf("container remoteEnv persisted=%t omit=%t entries=%+v", found, omit, entries)
					}
					if withFeature {
						img, err := cli.ImageInspect(ctx, info.Image)
						if err != nil {
							t.Fatal(err)
						}
						imageEntries, err := metadata.Parse(img.Config.Labels[metadata.Label])
						if err != nil {
							t.Fatal(err)
						}
						if len(imageEntries) != 2 || (len(imageEntries[1].RemoteEnv) == 0) != omit {
							t.Fatalf("image entries=%+v omit=%v", imageEntries, omit)
						}
					}
					var stdout, stderr bytes.Buffer
					// Read the lifecycle effect without injecting configured remoteEnv again.
					code := Run(ctx, []string{"exec", "--workspace-folder", workspace, "--", "cat", "/tmp/remote-marker"}, "test", nil, &stdout, &stderr)
					if code != 0 || stdout.String() != "configured" {
						t.Fatalf("lifecycle remoteEnv exit=%d output=%q logs=%s", code, stdout.String(), stderr.String())
					}

				})
			}
		}
	}
}

func TestBuildCustomizationAndSyntaxControlsDockerIntegration(t *testing.T) {
	t.Setenv("BUILDKIT_HOST", "")
	cli, err := newDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	ctx := context.Background()
	for _, mode := range []string{"image", "dockerfile", "compose-dockerfile"} {
		for _, skip := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/skip=%t", mode, skip), func(t *testing.T) {
				workspace := featureControlFixture(t, mode, true)
				tag := defaultImage(workspace)
				t.Cleanup(func() {
					_, _ = cli.ImageRemove(ctx, tag, imagetypes.RemoveOptions{Force: true})
					if mode == "compose-dockerfile" {
						_, _ = cli.ImageRemove(ctx, strings.Replace(tag, ":latest", ":base", 1), imagetypes.RemoveOptions{Force: true})
					}
				})
				args := []string{"build", "--workspace-folder", workspace}
				if skip {
					args = append(args, "--skip-persisting-customizations-from-features")
				}
				if strings.Contains(mode, "dockerfile") {
					args = append(args, "--omit-syntax-directive")
				}
				invoke(t, 0, args...)
				img, err := cli.ImageInspect(ctx, tag)
				if err != nil {
					t.Fatal(err)
				}
				entries, err := metadata.Parse(img.Config.Labels[metadata.Label])
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 2 || (len(entries[0].Customizations) == 0) != skip || entries[1].Customizations["editor"].(map[string]any)["value"] != "config" || entries[1].RemoteEnv["VALUE"] != "configured" {
					t.Fatalf("image metadata skip=%t entries=%+v", skip, entries)
				}
				// A successful image still contains the installed feature.
				created, err := cli.ContainerCreate(ctx, &container.Config{Image: tag, Cmd: []string{"cat", "/tmp/feature-marker"}}, nil, nil, nil, "")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_ = cli.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
				})
				// Copy does not start the container or run lifecycle commands.
				stream, _, err := cli.CopyFromContainer(ctx, created.ID, "/tmp/feature-marker")
				if err != nil {
					t.Fatal(err)
				}
				stream.Close()
			})
		}
	}
}
