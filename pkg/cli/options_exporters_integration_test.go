//go:build docker_integration

// relationships:
//
//	references: [options_exporters, ../builder/builder]
package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	imagetypes "github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Exercise the full CLI -> builder -> Engine BuildKit path with native output.
func TestBuildOutputIntegration(t *testing.T) {
	t.Setenv("BUILDKIT_HOST", "")
	dir := t.TempDir()
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	t.Cleanup(func() {
		_, _ = cli.ImageRemove(context.Background(), defaultImage(dir), imagetypes.RemoveOptions{Force: true})
	})
	for name, data := range map[string]string{"Dockerfile": "FROM alpine:3.20\nRUN printf exported > /message\n", "devcontainer.json": `{"build":{"dockerfile":"Dockerfile","context":"."}}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ name, output, member string }{
		{"directory", "result", ""},
		{"local", "type=local,dest=result", ""},
		{"tar", "type=tar,dest=result.tar", "message"},
		{"oci", "type=oci,dest=result.tar", "oci-layout"},
		{"docker", "type=docker,dest=result.tar", "manifest.json"},
		{"stdout", "type=tar,dest=-", "message"},
		{"stdout-short", "-", "message"},
		{"stdout-tar", "type=tar", "message"},
		{"stdout-oci", "type=oci", "oci-layout"},
		{"stdout-oci-explicit", "type=oci,dest=-", "oci-layout"},
		{"stdout-docker", "type=docker,dest=-", "manifest.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "result")
			raw := tc.output
			toStdout := strings.HasPrefix(tc.name, "stdout")
			switch tc.name {
			case "directory":
				raw = dest
			case "local":
				raw = "type=local,dest=" + dest
			case "stdout":
			default:
				if !toStdout {
					raw = "type=" + tc.name + ",dest=" + dest
				}
			}
			var stdout, stderr bytes.Buffer
			var writer io.Writer = &stdout
			var finish func()
			if toStdout {
				reader, pipe, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				defer pipe.Close()
				done := make(chan error, 1)
				go func() { _, err := io.Copy(&stdout, reader); done <- err }()
				writer = pipe
				finish = func() {
					pipe.Close()
					if err := <-done; err != nil {
						t.Fatal(err)
					}
				}
			}
			code := Run(context.Background(), []string{"build", "--workspace-folder", dir, "--config", filepath.Join(dir, "devcontainer.json"), "--skip-features", "--output", raw}, "test", bytes.NewReader(nil), writer, &stderr)
			if finish != nil {
				finish()
			}
			if code != 0 {
				t.Fatalf("exit %d: %s\n%s", code, stdout.String(), stderr.String())
			}
			if tc.member == "" {
				data, err := os.ReadFile(filepath.Join(dest, "message"))
				if err != nil || string(data) != "exported" {
					t.Fatalf("local %q %v", data, err)
				}
			} else {
				data := stdout.Bytes()
				if !toStdout {
					var err error
					data, err = os.ReadFile(dest)
					if err != nil {
						t.Fatal(err)
					}
				}
				found := false
				reader := bytes.NewReader(data)
				tr := tar.NewReader(reader)
				for {
					hdr, err := tr.Next()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if hdr.Name == tc.member {
						found = true
					}
				}
				trailing, err := io.ReadAll(reader)
				if err != nil || len(bytes.Trim(trailing, "\x00")) != 0 {
					t.Fatalf("unexpected bytes after archive: %q, %v", trailing, err)
				}
				if !found {
					t.Fatalf("archive lacks %s", tc.member)
				}
			}
			if !toStdout {
				var result map[string]any
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result["output"] != raw {
					t.Fatalf("result %s, %v", stdout.String(), err)
				}
			}
		})
	}
}

// Prove the destination-less exporters against the daemon store, and OCI
// directory export against its actual descriptor graph, rather than a plan.
func TestBuildOutputStoreIntegration(t *testing.T) {
	t.Setenv("BUILDKIT_HOST", "")
	ctx := context.Background()
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	info, err := cli.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("daemon driver=%s status=%v", info.Driver, info.DriverStatus)
	for _, kind := range []string{"docker", "image", "oci-layout", "cacheonly"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			tag := defaultImage(dir)
			if _, err := cli.ImageInspect(ctx, tag); !errdefs.IsNotFound(err) {
				t.Fatalf("test requires absent owned image %s: %v", tag, err)
			}
			t.Cleanup(func() { _, _ = cli.ImageRemove(ctx, tag, imagetypes.RemoveOptions{Force: true}) })
			for name, data := range map[string]string{"Dockerfile": "FROM alpine:3.20\nLABEL sample.marker=exported\nRUN printf exported > /message\n", "devcontainer.json": `{"build":{"dockerfile":"Dockerfile","context":"."}}`} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			destination := filepath.Join(t.TempDir(), "layout")
			raw := "type=" + kind
			if kind == "oci-layout" {
				raw = "type=oci,tar=false,dest=" + destination
			}
			var stdout, stderr bytes.Buffer
			code := Run(ctx, []string{"build", "--workspace-folder", dir, "--config", filepath.Join(dir, "devcontainer.json"), "--skip-features", "--image-name", tag, "--output", raw}, "test", bytes.NewReader(nil), &stdout, &stderr)
			if code != 0 {
				t.Fatalf("exit %d: %s\n%s", code, &stdout, &stderr)
			}
			var result map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result["outcome"] != "success" || result["output"] != raw {
				t.Fatalf("export must retain JSON result without archive bytes: %s (%v)", &stdout, err)
			}
			switch kind {
			case "docker", "image":
				image, err := cli.ImageInspect(ctx, tag)
				if err != nil {
					t.Fatalf("exported image must exist in Engine store: %v", err)
				}
				if image.Config == nil || image.Config.Labels["sample.marker"] != "exported" {
					t.Fatalf("exported image lacks expected metadata: %+v", image.Config)
				}
			case "oci-layout":
				assertOCILayout(t, destination)
			case "cacheonly":
				if _, err := cli.ImageInspect(ctx, tag); !errdefs.IsNotFound(err) {
					t.Fatalf("cache-only export must not load image: %v", err)
				}
				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 2 {
					t.Fatalf("cache-only export produced unexpected files: %v", entries)
				}
				if _, err := os.Stat(destination); !os.IsNotExist(err) {
					t.Fatalf("cache-only export produced a layout: %v", err)
				}
			}
		})
	}
}

func assertOCILayout(t *testing.T, dir string) {
	t.Helper()
	layout, err := os.ReadFile(filepath.Join(dir, "oci-layout"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata ocispec.ImageLayout
	if err := json.Unmarshal(layout, &metadata); err != nil || metadata.Version != "1.0.0" {
		t.Fatalf("OCI layout metadata %s: %v", layout, err)
	}
	indexData, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index ocispec.Index
	if err := json.Unmarshal(indexData, &index); err != nil || index.SchemaVersion != 2 || len(index.Manifests) == 0 {
		t.Fatalf("OCI index %s: %v", indexData, err)
	}
	var visit func(ocispec.Descriptor)
	visit = func(desc ocispec.Descriptor) {
		t.Helper()
		if err := desc.Digest.Validate(); err != nil {
			t.Fatal(err)
		}
		blob, err := os.ReadFile(filepath.Join(dir, "blobs", desc.Digest.Algorithm().String(), desc.Digest.Encoded()))
		if err != nil {
			t.Fatalf("referenced OCI blob must be readable: %v", err)
		}
		if int64(len(blob)) != desc.Size || digest.FromBytes(blob) != desc.Digest {
			t.Fatalf("OCI blob differs from descriptor: %+v", desc)
		}
		switch desc.MediaType {
		case ocispec.MediaTypeImageIndex, "application/vnd.docker.distribution.manifest.list.v2+json":
			var nested ocispec.Index
			if err := json.Unmarshal(blob, &nested); err != nil || len(nested.Manifests) == 0 {
				t.Fatalf("nested OCI index: %v", err)
			}
			for _, child := range nested.Manifests {
				visit(child)
			}
		case ocispec.MediaTypeImageManifest, "application/vnd.docker.distribution.manifest.v2+json":
			var manifest ocispec.Manifest
			if err := json.Unmarshal(blob, &manifest); err != nil || len(manifest.Layers) == 0 {
				t.Fatalf("OCI manifest: %v", err)
			}
			visit(manifest.Config)
			for _, layer := range manifest.Layers {
				visit(layer)
			}
		}
	}
	for _, desc := range index.Manifests {
		visit(desc)
	}
}
