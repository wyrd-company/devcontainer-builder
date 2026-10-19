// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// secretsFixture writes a configuration and a secrets file holding the
// sentinel, and returns their paths.
func secretsFixture(t *testing.T) (dir, config, secrets string) {
	t.Helper()
	dir = t.TempDir()
	config = filepath.Join(dir, "devcontainer.json")
	if err := os.WriteFile(config, []byte(`{"image":"alpine:3.20","updateRemoteUserUID":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]string{"SAMPLE_TOKEN": sampleSecret})
	if err != nil {
		t.Fatal(err)
	}
	secrets = filepath.Join(dir, "secrets.json")
	if err := os.WriteFile(secrets, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, config, secrets
}

func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, "test", nil, &out, &errOut)
	return code, out.String(), errOut.String()
}

// A rejected --remote-env names the flag and its position, never the value,
// and secrets are masked before the option is validated.
func TestRemoteEnvErrorDoesNotEchoValues(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	_, _, secrets := secretsFixture(t)
	for _, command := range []string{"up", "run-user-commands"} {
		code, stdout, stderr := runCLI(command, "--secrets-file", secrets, "--remote-env", "SAMPLE=ok", "--remote-env", sampleSecret)
		if code != 2 {
			t.Fatalf("%s: exit %d", command, code)
		}
		var result map[string]any
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatal(err)
		}
		if want := "--remote-env value 2 must be KEY=VALUE"; result["message"] != want {
			t.Errorf("%s: message %q, want %q", command, result["message"], want)
		}
		assertNoSecret(t, command+" stdout", stdout)
		assertNoSecret(t, command+" stderr", stderr)
	}
}

// The other repeated KEY=VALUE flags report a bad value the same way.
func TestPairFlagErrorsName(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	_, config, _ := secretsFixture(t)
	for _, flag := range []string{"build-arg", "label"} {
		code, stdout, _ := runCLI("up", "--config", config, "--"+flag, "SAMPLE=ok", "--"+flag, sampleSecret)
		var result map[string]any
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatal(err)
		}
		if want := "--" + flag + " value 2 must be KEY=VALUE"; code != 2 || result["message"] != want {
			t.Errorf("--%s: exit %d, message %q, want %q", flag, code, result["message"], want)
		}
	}
}

// Flag parsing runs before any command, and its errors quote the rejected
// value. The mask is installed before parsing, whichever side of the bad
// flag --secrets-file is on.
func TestFlagParseErrorIsMasked(t *testing.T) {
	_, _, secrets := secretsFixture(t)
	for _, args := range [][]string{
		{"up", "--secrets-file", secrets, "--skip-post-create=" + sampleSecret},
		{"up", "--skip-post-create=" + sampleSecret, "--secrets-file", secrets},
		{"run-user-commands", "--skip-post-attach=" + sampleSecret, "--secrets-file=" + secrets},
	} {
		code, stdout, stderr := runCLI(args...)
		if code != 2 || !strings.Contains(stdout, secretMaskText) {
			t.Errorf("%v: exit %d, want 2 and a masked parse error: %s", args[:2], code, stdout)
		}
		assertNoSecret(t, "stdout", stdout)
		assertNoSecret(t, "stderr", stderr)
	}
}

// The secrets file is read once, so that a pipe such as /dev/stdin works.
func TestSecretsFileReadOnce(t *testing.T) {
	a := &application{}
	path := writeSecretsFile(t, `{"SAMPLE": "one"}`)
	a.prescanSecrets([]string{"up", "--secrets-file", path})
	if err := os.WriteFile(path, []byte(`{"SAMPLE": "two"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := a.secretsFile(path)
	if err != nil || got["SAMPLE"] != "one" {
		t.Fatalf("secrets = %v, %v; want the first read", got, err)
	}
	if a.mask.String("one") != secretMaskText {
		t.Error("the pre-scan did not install the mask")
	}
}

// The pre-scan stops at "--", so that a container command's arguments are
// not read as a secrets file.
func TestPrescanStopsAtArgumentBoundary(t *testing.T) {
	a := &application{}
	path := writeSecretsFile(t, `{"SAMPLE": "one"}`)
	a.prescanSecrets([]string{"exec", "--", "sh", "--secrets-file", path})
	if a.mask != nil || len(a.secretFiles) != 0 {
		t.Fatal("the pre-scan read a file named after --")
	}
}
