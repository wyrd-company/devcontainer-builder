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
}

func (o *environmentOptions) flags(cmd *cobra.Command, secrets bool) {
	cmd.Flags().StringArrayVar(&o.remoteEnv, "remote-env", nil, "Remote environment KEY=VALUE for lifecycle commands and dotfiles (repeat)")
	if secrets {
		cmd.Flags().StringVar(&o.secretsFile, "secrets-file", "", "JSON file of secret environment variables for lifecycle commands and dotfiles")
	}
}

// load parses the flags. From here on, secret values are masked in stderr
// and in the result.
func (o *environmentOptions) load(a *application) (remote, secrets map[string]string, err error) {
	remote, err = parsePairs(o.remoteEnv)
	if err != nil {
		return nil, nil, err
	}
	secrets, err = readSecretsFile(o.secretsFile)
	if err != nil {
		return nil, nil, err
	}
	a.maskSecrets(secrets)
	return remote, secrets, nil
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
