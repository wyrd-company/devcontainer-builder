package registryauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"

	"github.com/docker/docker-credential-helpers/credentials"
)

// helperOutputLimit bounds the answer read from a credential helper. A
// protocol answer is a small JSON object; tokens of a few kilobytes fit
// with a wide margin.
const helperOutputLimit = 1 << 20

// helperNamePattern is the charset of a credsStore or credHelpers value:
// one executable name suffix, with no path separator.
var helperNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// tokenUsername is the helper protocol's username for an identity token.
const tokenUsername = "<token>"

// validHelperName rejects a helper name that is not a plain suffix of
// docker-credential-<name>, so that it cannot name a path.
func validHelperName(name string) error {
	if !helperNamePattern.MatchString(name) || strings.Contains(name, "..") {
		return fmt.Errorf("invalid credential helper name %q", name)
	}
	return nil
}

// helperCredentials runs docker-credential-<name> get for serverURL, as
// the credential-helper protocol defines. The program is looked up on
// PATH only. Its stderr goes to the null device and its stdout is read up
// to helperOutputLimit, so that no helper output reaches the process
// output or an error. A helper that does not know serverURL is anonymous.
func helperCredentials(name, serverURL string) (Credentials, error) {
	if err := validHelperName(name); err != nil {
		return Credentials{}, err
	}
	program := "docker-credential-" + name
	path, err := exec.LookPath(program)
	if err != nil {
		return Credentials{}, fmt.Errorf("credential helper %s not found on PATH", program)
	}
	out, err := runHelper(path, serverURL)
	if errors.Is(err, errHelperOutputLimit) {
		return Credentials{}, fmt.Errorf("credential helper %s: %w", program, err)
	}
	if err != nil {
		if credentials.IsErrCredentialsNotFoundMessage(strings.TrimSpace(string(out))) {
			return Credentials{}, nil
		}
		return Credentials{}, fmt.Errorf("credential helper %s get failed: %s", program, exitReason(err))
	}
	var resp credentials.Credentials
	if err := json.Unmarshal(out, &resp); err != nil {
		return Credentials{}, fmt.Errorf("credential helper %s returned a malformed answer", program)
	}
	if resp.Username == tokenUsername {
		return Credentials{IdentityToken: resp.Secret}, nil
	}
	return Credentials{Username: resp.Username, Password: resp.Secret}, nil
}

var errHelperOutputLimit = fmt.Errorf("output exceeds %d bytes", helperOutputLimit)

// runHelper runs path get with serverURL on stdin and returns its bounded
// stdout. When the output exceeds the bound, the helper is killed.
func runHelper(path, serverURL string) ([]byte, error) {
	cmd := exec.Command(path, credentials.ActionGet)
	cmd.Stdin = strings.NewReader(serverURL)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	_, readErr := io.Copy(&out, io.LimitReader(stdout, helperOutputLimit+1))
	if out.Len() > helperOutputLimit {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errHelperOutputLimit
	}
	if err := cmd.Wait(); err != nil {
		return out.Bytes(), err
	}
	return out.Bytes(), readErr
}

// exitReason is a helper failure without any of its output.
func exitReason(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ProcessState.String()
	}
	return "it could not be run"
}
