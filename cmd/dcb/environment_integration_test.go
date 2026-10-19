//go:build docker_integration

// relationships:
//
//	references: [CONTRIBUTING]
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	dockerclient "github.com/docker/docker/client"
)

// environmentSecret holds characters whose quoted and JSON spellings differ
// from the raw value.
const environmentSecret = `sample"secret<7f3c>`

// environmentHook records each lifecycle environment without writing a
// secret: secret values are stored as digests. It also prints the secret, so
// that stderr shows whether dcb masks lifecycle output.
const environmentHook = `printf '%s|%s|%s|%s|%s\n' "$SAMPLE_REMOTE" "$SAMPLE_SHARED" "$SAMPLE_CONFIG" ` +
	`"$(printf %s "$SAMPLE_SECRET" | sha256sum | cut -d' ' -f1)" "$(printf %s "$SAMPLE_OVERRIDDEN" | sha256sum | cut -d' ' -f1)" > /tmp/sample-env; ` +
	`echo "lifecycle-secret=$SAMPLE_SECRET"`

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func environmentLeakSpellings() []string {
	quoted := strconv.Quote(environmentSecret)
	encoded, _ := json.Marshal(environmentSecret)
	b := []byte(environmentSecret)
	return []string{environmentSecret, quoted[1 : len(quoted)-1], string(encoded[1 : len(encoded)-1]),
		base64.StdEncoding.EncodeToString(b), base64.RawStdEncoding.EncodeToString(b),
		base64.URLEncoding.EncodeToString(b), base64.RawURLEncoding.EncodeToString(b),
		url.QueryEscape(environmentSecret), url.PathEscape(environmentSecret)}
}

// leaks returns the spellings of the secret that data contains.
func leaks(data []byte) []string {
	var found []string
	for _, spelling := range environmentLeakSpellings() {
		if bytes.Contains(data, []byte(spelling)) {
			found = append(found, spelling)
		}
	}
	return found
}

type environmentFixture struct {
	bin, workspace, tag, secrets string
	outputs                      map[string][]byte
}

func newEnvironmentFixture(t *testing.T, bin string, compose bool) *environmentFixture {
	t.Helper()
	workspace := hostPrepareWorkspace(t, "alpine:3.20")
	configDir := filepath.Join(workspace, ".devcontainer")
	featureDir := filepath.Join(configDir, "sample")
	if err := os.MkdirAll(featureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"devcontainer-feature.json": `{"id":"sample","version":"1.0.0"}`,
		"install.sh":                "#!/bin/sh\ntrue\n",
	} {
		if err := os.WriteFile(filepath.Join(featureDir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	config := map[string]any{
		"features":          map[string]any{"./sample": map[string]any{}},
		"remoteEnv":         map[string]string{"SAMPLE_SHARED": "config", "SAMPLE_CONFIG": "config", "SAMPLE_OVERRIDDEN": "config"},
		"postAttachCommand": environmentHook,
	}
	if compose {
		config["dockerComposeFile"], config["service"], config["workspaceFolder"] = "compose.yml", "app", "/workspace"
		yml := "services:\n  app:\n    image: alpine:3.20\n    volumes: [\"..:/workspace\"]\n    command: [sleep, infinity]\n"
		if err := os.WriteFile(filepath.Join(configDir, "compose.yml"), []byte(yml), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		config["image"], config["updateRemoteUserUID"] = "alpine:3.20", false
	}
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "devcontainer.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	// The secrets file lives outside the workspace, which is scanned.
	secrets, err := json.Marshal(map[string]string{"SAMPLE_SECRET": environmentSecret, "SAMPLE_OVERRIDDEN": environmentSecret})
	if err != nil {
		t.Fatal(err)
	}
	secretsPath := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.WriteFile(secretsPath, secrets, 0o600); err != nil {
		t.Fatal(err)
	}
	f := &environmentFixture{bin: bin, workspace: workspace, tag: fmt.Sprintf("dcb-test/environment:%d", time.Now().UnixNano()), secrets: secretsPath, outputs: map[string][]byte{}}
	t.Cleanup(func() {
		if err := hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace); err != nil {
			t.Logf("down: %v", err)
		}
		removeImage(t, f.tag)
	})
	return f
}

// run invokes dcb, requires success, and keeps stdout and stderr for the scan.
func (f *environmentFixture) run(t *testing.T, name string, args ...string) (map[string]any, string) {
	t.Helper()
	cmd := exec.Command(f.bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	f.outputs[name+" stdout"], f.outputs[name+" stderr"] = stdout.Bytes(), stderr.Bytes()
	var result map[string]any
	if decodeErr := json.Unmarshal(stdout.Bytes(), &result); err != nil || decodeErr != nil || result["outcome"] != "success" {
		t.Fatalf("%v: %v, result %s\nstderr: %s", args, err, stdout.String(), stderr.String())
	}
	return result, stderr.String()
}

func (f *environmentFixture) recordedEnv(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(string(hostRunCommand(t, os.Environ(), f.bin, "exec", "--workspace-folder", f.workspace, "--", "cat", "/tmp/sample-env")))
}

// TestEnvironmentOptions passes --remote-env and --secrets-file through up,
// run-user-commands and set-up in both host modes. It then scans every
// output, the container and image metadata, the container filesystem and
// the workspace for the secret.
func TestEnvironmentOptions(t *testing.T) {
	ctx := context.Background()
	cli := hostRequireDocker(t, ctx)
	t.Cleanup(func() { _ = cli.Close() })
	hostEnsureImage(t, ctx, cli, "alpine:3.20")
	bin := hostBuildBinary(t)
	secret, config, none := digest(environmentSecret), digest("config"), digest("")
	for _, mode := range []string{"image", "compose"} {
		t.Run(mode, func(t *testing.T) {
			f := newEnvironmentFixture(t, bin, mode == "compose")

			// --remote-env loses to remoteEnv for the same name; secrets win over both.
			result, stderr := f.run(t, "up", "up", "--workspace-folder", f.workspace, "--image-name", f.tag,
				"--remote-env", "SAMPLE_REMOTE=up", "--remote-env", "SAMPLE_SHARED=up", "--secrets-file", f.secrets,
				"--include-configuration", "--include-merged-configuration")
			assertConfigurationWithoutSecret(t, result)
			if got, want := f.recordedEnv(t), "up|config|config|"+secret+"|"+secret; got != want {
				t.Fatalf("up lifecycle env = %q, want %q", got, want)
			}
			if !strings.Contains(stderr, "lifecycle-secret=********\n") {
				t.Fatalf("up stderr lacks the masked lifecycle output:\n%s", stderr)
			}
			id := result["containerId"].(string)

			_, stderr = f.run(t, "run-user-commands", "run-user-commands", "--workspace-folder", f.workspace, "--log-format", "json",
				"--remote-env", "SAMPLE_REMOTE=run", "--secrets-file", f.secrets)
			if got, want := f.recordedEnv(t), "run|config|config|"+secret+"|"+secret; got != want {
				t.Fatalf("run-user-commands lifecycle env = %q, want %q", got, want)
			}
			if !strings.Contains(stderr, `"text":"lifecycle-secret=********"`) {
				t.Fatalf("run-user-commands stderr lacks the masked lifecycle record:\n%s", stderr)
			}

			f.run(t, "set-up", "set-up", "--workspace-folder", f.workspace, "--container-id", id, "--remote-env", "SAMPLE_REMOTE=setup")
			if got, want := f.recordedEnv(t), "setup|config|config|"+none+"|"+config; got != want {
				t.Fatalf("set-up lifecycle env = %q, want %q", got, want)
			}

			// exec takes no secrets; up's remote environment is not stored.
			out := hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", f.workspace, "--", "sh", "-c", `printf '%s|%s' "$SAMPLE_REMOTE" "$SAMPLE_SECRET"`)
			if string(out) != "|" {
				t.Fatalf("exec env = %q, want neither remote env nor secret", out)
			}

			for name, data := range f.outputs {
				if found := leaks(data); len(found) > 0 {
					t.Errorf("%s contains the secret as %q:\n%s", name, found, data)
				}
			}
			f.scanContainer(t, ctx, cli, id)
			f.scanWorkspace(t)
		})
	}
}

// assertConfigurationWithoutSecret checks the configuration snapshots of a
// result. dcb masks every result string that equals a secret, so the mask
// must be absent too: a masked value would mean the secret reached them.
func assertConfigurationWithoutSecret(t *testing.T, result map[string]any) {
	t.Helper()
	for _, key := range []string{"configuration", "mergedConfiguration"} {
		value, ok := result[key].(map[string]any)
		if !ok || len(value) == 0 {
			t.Fatalf("result %s = %#v, want a configuration object", key, result[key])
		}
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte("SAMPLE_OVERRIDDEN")) {
			t.Fatalf("result %s lacks the configuration remoteEnv: %s", key, data)
		}
		if found := leaks(data); len(found) > 0 || bytes.Contains(data, []byte("********")) {
			t.Errorf("result %s holds the secret (spellings %q) or its mask: %s", key, found, data)
		}
	}
}

// scanContainer scans the container and image inspection and the exported
// container filesystem. A planted value proves that the export reaches the
// files that lifecycle commands write.
func (f *environmentFixture) scanContainer(t *testing.T, ctx context.Context, cli *dockerclient.Client, id string) {
	t.Helper()
	info, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	image, err := cli.ImageInspect(ctx, info.Image)
	if err != nil {
		t.Fatal(err)
	}
	if image.Config == nil || image.Config.Labels["devcontainer.metadata"] == "" {
		t.Fatalf("image %s has no devcontainer.metadata label", info.Image)
	}
	for name, v := range map[string]any{"container inspection": info, "image inspection": image} {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if found := leaks(data); len(found) > 0 {
			t.Errorf("%s contains the secret as %q", name, found)
		}
	}
	filesystem := exportContainer(t, ctx, cli, id)
	if !bytes.Contains(filesystem, []byte("setup|config|config|")) {
		t.Fatal("container export lacks /tmp/sample-env; the scan did not reach lifecycle files")
	}
	if found := leaks(filesystem); len(found) > 0 {
		t.Errorf("container filesystem contains the secret as %q", found)
	}
	hostRunCommand(t, os.Environ(), f.bin, "exec", "--workspace-folder", f.workspace, "--remote-env", "SAMPLE_PLANTED="+environmentSecret,
		"--", "sh", "-c", `printf %s "$SAMPLE_PLANTED" > /tmp/sample-planted`)
	if found := leaks(exportContainer(t, ctx, cli, id)); len(found) == 0 {
		t.Fatal("container export scan did not find a planted secret")
	}
}

func exportContainer(t *testing.T, ctx context.Context, cli *dockerclient.Client, id string) []byte {
	t.Helper()
	rc, err := cli.ContainerExport(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// scanWorkspace scans every file in the workspace, which up bind-mounts.
func (f *environmentFixture) scanWorkspace(t *testing.T) {
	t.Helper()
	files := 0
	err := filepath.WalkDir(f.workspace, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		if found := leaks(data); len(found) > 0 {
			t.Errorf("workspace file %s contains the secret as %q", path, found)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("workspace scan read no files")
	}
}
