// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// environmentOptions are upstream's remote environment flags on up, set-up
// and run-user-commands. Upstream declares --secrets-file on up and
// run-user-commands only.
type environmentOptions struct {
	remoteEnv   []string
	secretsFile string
	probe       userEnvProbeOptions
}

func (o *environmentOptions) flags(cmd *cobra.Command, secrets bool) {
	o.probe.flags(cmd)
	cmd.Flags().StringArrayVar(&o.remoteEnv, "remote-env", nil, "Remote environment KEY=VALUE for lifecycle commands and dotfiles (repeat)")
	if secrets {
		cmd.Flags().StringVar(&o.secretsFile, "secrets-file", "", "JSON file of secret environment variables for lifecycle commands and dotfiles")
	}
}

// load reads the secrets file before it validates --remote-env, so that the
// mask covers every error after it.
func (o *environmentOptions) load(a *application) (remote, secrets map[string]string, err error) {
	secrets, err = a.secretsFile(o.secretsFile)
	if err != nil {
		return nil, nil, err
	}
	a.maskSecrets(secrets)
	if err = o.probe.validate(); err != nil {
		return nil, nil, err
	}
	remote, err = parsePairs("remote-env", o.remoteEnv)
	if err != nil {
		return nil, nil, err
	}
	return remote, secrets, nil
}

type secretsFileRead struct {
	secrets map[string]string
	err     error
}

// prescanSecrets reads each --secrets-file in args and masks its values
// before flag parsing, whose errors quote rejected values. It stops at "--".
// A file that cannot be read is reported when the command loads its flag.
func (a *application) prescanSecrets(args []string) {
	for i, arg := range args {
		path, ok := strings.CutPrefix(arg, "--secrets-file=")
		switch {
		case arg == "--":
			return
		case arg == "--secrets-file" && i+1 < len(args):
			path, ok = args[i+1], true
		}
		if ok {
			if secrets, err := a.secretsFile(path); err == nil {
				a.maskSecrets(secrets)
			}
		}
	}
}

// secretsFile reads each path once, so that a pipe such as /dev/stdin
// works and the mask and the lifecycle use the same values.
func (a *application) secretsFile(path string) (map[string]string, error) {
	if read, ok := a.secretFiles[path]; ok {
		return read.secrets, read.err
	}
	secrets, err := readSecretsFile(path)
	if a.secretFiles == nil {
		a.secretFiles = map[string]secretsFileRead{}
	}
	a.secretFiles[path] = secretsFileRead{secrets, err}
	return secrets, err
}

// readSecretsFile reads upstream's secrets file: a JSON (with comments)
// object of names to values. Upstream puts each value in the exec
// environment with a template literal, so a number or boolean becomes its
// JavaScript text. An empty path means no secrets, as upstream. Errors name
// the path and a fixed reason, never file content.
func readSecretsFile(path string) (map[string]string, error) {
	if path == "" {
		return nil, nil
	}
	fail := func(reason string) error { return fmt.Errorf("read secrets file %q: %s", path, reason) }
	data, err := os.ReadFile(path)
	if err != nil {
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			return nil, fail(pathErr.Err.Error())
		}
		return nil, fail("the file cannot be read")
	}
	var doc any
	if err := json.Unmarshal(spec.StripJSONC(data), &doc); err != nil {
		return nil, fail("the file is not valid JSON")
	}
	if doc == nil {
		return nil, nil
	}
	object, ok := doc.(map[string]any)
	if !ok {
		return nil, fail("the file must contain a JSON object")
	}
	secrets := make(map[string]string, len(object))
	for name, value := range object {
		switch value := value.(type) {
		case string:
			secrets[name] = value
		case float64:
			secrets[name] = jsNumber(value)
		case bool:
			secrets[name] = strconv.FormatBool(value)
		default:
			return nil, fail("every value must be a JSON string, number or boolean")
		}
	}
	return secrets, nil
}

// jsNumber formats f as JavaScript's Number ToString does: the shortest
// round-trip digits, in plain notation for exponents from -7 to 20.
func jsNumber(f float64) string {
	if f == 0 {
		return "0"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	x, _ := strconv.Atoi(exponent)
	n, k := x+1, len(digits)
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	text := sign + digits[:1]
	if k > 1 {
		text += "." + digits[1:]
	}
	if n > 0 {
		return text + "e+" + strconv.Itoa(n-1)
	}
	return text + "e-" + strconv.Itoa(1-n)
}
