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
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// sampleSecret is chosen so that each of its leak spellings differs from the
// others: it has a quote, a space, HTML-escaped characters, and bytes that
// standard and URL base64 encode differently.
const sampleSecret = `sample"secret <7f3c>?~`

// leakSpellings are the forms in which a leaked value could appear. They are
// computed here, independently of the mask, so that a spelling the mask
// misses is found.
func leakSpellings(value string) []string {
	quoted := strconv.Quote(value)
	encoded, _ := json.Marshal(value)
	b := []byte(value)
	return []string{value, quoted[1 : len(quoted)-1], string(encoded[1 : len(encoded)-1]),
		base64.StdEncoding.EncodeToString(b), base64.RawStdEncoding.EncodeToString(b),
		base64.URLEncoding.EncodeToString(b), base64.RawURLEncoding.EncodeToString(b),
		url.QueryEscape(value), url.PathEscape(value)}
}

// The sentinel's spellings must be distinct, or a test that injects one
// spelling would also cover another by accident.
func TestSampleSecretSpellingsDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range leakSpellings(sampleSecret) {
		if seen[s] {
			t.Errorf("spelling %q repeats", s)
		}
		seen[s] = true
	}
}

func assertNoSecret(t *testing.T, channel, text string) {
	t.Helper()
	for _, spelling := range leakSpellings(sampleSecret) {
		if strings.Contains(text, spelling) {
			t.Errorf("%s contains the secret as %q: %s", channel, spelling, text)
		}
	}
}

func writeSecretsFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Upstream declares --remote-env on up, set-up and run-user-commands, and
// --secrets-file on up and run-user-commands.
func TestEnvironmentFlagSurface(t *testing.T) {
	a := &application{}
	for name, tc := range map[string]struct {
		cmd     *cobra.Command
		secrets bool
	}{
		"up":                {a.upCommand(), true},
		"set-up":            {a.lifecycleCommand("set-up"), false},
		"run-user-commands": {a.lifecycleCommand("run-user-commands"), true},
	} {
		remote := tc.cmd.Flags().Lookup("remote-env")
		if remote == nil || remote.Value.Type() != "stringArray" || remote.DefValue != "[]" || remote.Hidden {
			t.Errorf("%s --remote-env = %+v, want a visible repeatable string", name, remote)
		}
		secrets := tc.cmd.Flags().Lookup("secrets-file")
		if (secrets != nil) != tc.secrets {
			t.Errorf("%s --secrets-file registered = %v, want %v", name, secrets != nil, tc.secrets)
		}
		if secrets != nil && (secrets.Value.Type() != "string" || secrets.DefValue != "" || secrets.Hidden) {
			t.Errorf("%s --secrets-file = %+v, want a visible string without a default", name, secrets)
		}
	}
	if a.execCommand().Flags().Lookup("secrets-file") != nil {
		t.Error("exec registers --secrets-file; upstream exec has none")
	}
}

// A --remote-env without a name is an invalid invocation, rejected before
// Docker is reached. The error names the flag and position, not the value.
func TestRemoteEnvFormat(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	for _, args := range [][]string{
		{"up"},
		{"set-up", "--container-id", "sample"},
		{"run-user-commands"},
		{"exec", "--container-id", "sample"},
	} {
		for _, value := range []string{"SAMPLE", "=value"} {
			argv := append(append([]string(nil), args...), "--remote-env", value)
			if args[0] == "exec" {
				argv = append(argv, "--", "true")
			}
			result, _ := invoke(t, 2, argv...)
			if result["message"] != "--remote-env value 1 must be KEY=VALUE" {
				t.Errorf("%v --remote-env %q: %v", args, value, result)
			}
		}
	}
}

func TestEnvironmentLoad(t *testing.T) {
	a := &application{logs: &logWriter{dst: &bytes.Buffer{}}}
	o := environmentOptions{
		remoteEnv:   []string{"SAMPLE_A=one=two", "SAMPLE_B=", "SAMPLE_A=three"},
		secretsFile: writeSecretsFile(t, `{"SAMPLE_SECRET": "`+strings.ReplaceAll(sampleSecret, `"`, `\"`)+`"}`),
	}
	remote, secrets, err := o.load(a)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"SAMPLE_A": "three", "SAMPLE_B": ""}; !reflect.DeepEqual(remote, want) {
		t.Errorf("remote env = %v, want %v", remote, want)
	}
	if want := map[string]string{"SAMPLE_SECRET": sampleSecret}; !reflect.DeepEqual(secrets, want) {
		t.Errorf("secrets = %v, want %v", secrets, want)
	}
	if a.mask == nil || a.logs.mask != a.mask {
		t.Fatal("load did not install the secret mask on the application and its log")
	}
}

// Upstream parses the file as JSON with comments into an object of names
// to values, and an empty path is no secrets.
func TestReadSecretsFile(t *testing.T) {
	got, err := readSecretsFile(writeSecretsFile(t, "// sample\n{\"SAMPLE_A\": \"one\", /* two */ \"SAMPLE_B\": \"\", \"SAMPLE_A\": \"three\",}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"SAMPLE_A": "three", "SAMPLE_B": ""}; !reflect.DeepEqual(got, want) {
		t.Errorf("secrets = %v, want %v", got, want)
	}
	for _, body := range []string{"null", "{}"} {
		if got, err := readSecretsFile(writeSecretsFile(t, body)); err != nil || len(got) != 0 {
			t.Errorf("%s: secrets = %v, %v; want none", body, got, err)
		}
	}
	if got, err := readSecretsFile(""); err != nil || got != nil {
		t.Errorf("empty path: %v, %v", got, err)
	}
}

// Upstream puts a value in the exec environment with a template literal, so
// a JSON number or boolean becomes its JavaScript text. The expectations are
// node's String(JSON.parse(literal)) for each literal.
func TestReadSecretsFileCoercesScalars(t *testing.T) {
	for _, tc := range []struct{ literal, want string }{
		{"0", "0"},
		{"-0", "0"},
		{"1", "1"},
		{"1.0", "1"},
		{"1.5", "1.5"},
		{"-2.25", "-2.25"},
		{"0.1", "0.1"},
		{"100", "100"},
		{"1E3", "1000"},
		{"1e20", "100000000000000000000"},
		{"1e21", "1e+21"},
		{"123456789012345678901", "123456789012345680000"},
		{"0.000001", "0.000001"},
		{"1e-6", "0.000001"},
		{"1e-7", "1e-7"},
		{"1.5e-7", "1.5e-7"},
		{"123e-20", "1.23e-18"},
		{"9007199254740993", "9007199254740992"},
		{"5e-324", "5e-324"},
		{"1.7976931348623157e308", "1.7976931348623157e+308"},
		{"-1e-7", "-1e-7"},
		{"4.35", "4.35"},
		{"0.30000000000000004", "0.30000000000000004"},
		{"true", "true"},
		{"false", "false"},
	} {
		got, err := readSecretsFile(writeSecretsFile(t, `{"SAMPLE": `+tc.literal+`}`))
		if err != nil || got["SAMPLE"] != tc.want {
			t.Errorf("%s: secrets = %q, %v; want %q", tc.literal, got, err, tc.want)
		}
	}
}

// A malformed or unreadable file reports the path and a fixed reason. The
// file content, including a secret in any spelling, never reaches the error.
func TestReadSecretsFileErrors(t *testing.T) {
	escaped := strings.ReplaceAll(sampleSecret, `"`, `\"`)
	dir := t.TempDir()
	for _, tc := range []struct {
		name, body, reason string
	}{
		{"syntax", `{"SAMPLE": "` + escaped + `" "` + escaped + `"}`, "the file is not valid JSON"},
		{"unterminated", `{"SAMPLE": "` + escaped, "the file is not valid JSON"},
		{"bare value", `{"SAMPLE": ` + sampleSecret + `}`, "the file is not valid JSON"},
		{"trailing data", `{"SAMPLE": "value"} "` + escaped + `"`, "the file is not valid JSON"},
		{"array", `["` + escaped + `"]`, "the file must contain a JSON object"},
		{"string", `"` + escaped + `"`, "the file must contain a JSON object"},
		{"object value", `{"SAMPLE": {"nested": "` + escaped + `"}}`, "every value must be a JSON string, number or boolean"},
		{"array value", `{"SAMPLE": ["` + escaped + `"]}`, "every value must be a JSON string, number or boolean"},
		{"null value", `{"SAMPLE": null, "OTHER": "` + escaped + `"}`, "every value must be a JSON string, number or boolean"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeSecretsFile(t, tc.body)
			_, err := readSecretsFile(path)
			if err == nil {
				t.Fatal("malformed secrets file accepted")
			}
			if want := "read secrets file " + strconv.Quote(path) + ": " + tc.reason; err.Error() != want {
				t.Errorf("error = %q, want %q", err, want)
			}
			assertNoSecret(t, "error", err.Error())
		})
	}
	missing := filepath.Join(dir, "missing.json")
	if _, err := readSecretsFile(missing); err == nil || err.Error() != "read secrets file "+strconv.Quote(missing)+": no such file or directory" {
		t.Errorf("missing file: %v", err)
	}
	if _, err := readSecretsFile(dir); err == nil || err.Error() != "read secrets file "+strconv.Quote(dir)+": is a directory" {
		t.Errorf("directory: %v", err)
	}
}

// A malformed secrets file is an operation failure, as upstream, reported
// before the configuration or Docker is read.
func TestSecretsFileErrorResult(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	path := writeSecretsFile(t, `{"SAMPLE": "`+strings.ReplaceAll(sampleSecret, `"`, `\"`)+`",`)
	for _, command := range []string{"up", "run-user-commands"} {
		for _, format := range []string{"text", "json"} {
			result, stderr := invoke(t, 1, command, "--log-format", format, "--log-level", "trace", "--config", filepath.Join(t.TempDir(), "missing.json"), "--secrets-file", path)
			if want := "read secrets file " + strconv.Quote(path) + ": the file is not valid JSON"; result["message"] != want {
				t.Errorf("%s: message = %v, want %q", command, result["message"], want)
			}
			encoded, _ := json.Marshal(result)
			assertNoSecret(t, command+" result", string(encoded))
			assertNoSecret(t, command+" stderr", stderr)
		}
	}
}

// Once loaded, a secret value is masked in the result, here in an error
// that names a path holding the value.
func TestSecretMaskedInResult(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	const value = "sample-secret-path"
	secrets := writeSecretsFile(t, `{"SAMPLE": "`+value+`"}`)
	config := filepath.Join(t.TempDir(), value, "devcontainer.json")
	for _, command := range []string{"up", "run-user-commands"} {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{command, "--log-level", "trace", "--config", config, "--secrets-file", secrets}, "test", nil, &stdout, &stderr)
		if code != 1 || strings.Contains(stdout.String()+stderr.String(), value) {
			t.Fatalf("%s: exit %d, secret in output:\nstdout=%s\nstderr=%s", command, code, stdout.String(), stderr.String())
		}
		var result map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if message, _ := result["message"].(string); !strings.Contains(message, secretMaskText) {
			t.Errorf("%s: message = %q, want the masked path", command, message)
		}
	}
}
