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
	"testing"

	imagetypes "github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "result")
			raw := tc.output
			switch tc.name {
			case "directory":
				raw = dest
			case "local":
				raw = "type=local,dest=" + dest
			case "stdout":
			default:
				raw = "type=" + tc.name + ",dest=" + dest
			}
			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), []string{"build", "--workspace-folder", dir, "--config", filepath.Join(dir, "devcontainer.json"), "--skip-features", "--output", raw}, "test", bytes.NewReader(nil), &stdout, &stderr)
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
				if tc.name != "stdout" {
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
			if tc.name != "stdout" {
				var result map[string]any
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result["output"] != raw {
					t.Fatalf("result %s, %v", stdout.String(), err)
				}
			}
		})
	}
}
