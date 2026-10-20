// relationships:
//
//	references: [probe, ../../../reference/cli/src/spec-common/injectHeadless]
package runner

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
)

// PatchSystemEnv applies upstream's setupInContainer patches in order. Each
// marker is created before its edit, with noclobber, so a container is patched
// only once. A marker that cannot be created skips the edit, as upstream does.
func PatchSystemEnv(ctx context.Context, execer Execer, folder string, env map[string]string) error {
	if folder == "" {
		folder = "/var/devcontainer"
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var environment strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&environment, "\n%s=\"%s\"", key, env[key])
	}
	environment.WriteByte('\n')
	for _, patch := range []struct{ name, script, input string }{
		{"patchEtcEnvironment", "cat >> /etc/environment", environment.String()},
		{"patchEtcProfile", `sed -i -E 's/((^|\s)PATH=)([^\$]*)$/\1${PATH:-\3}/g' /etc/profile || true`, ""},
	} {
		marker := path.Join(folder, "."+patch.name+"Marker")
		script := "if test -f " + shellSQ(marker) + "; then exit 0; fi\n" +
			"(test ! -f " + shellSQ(marker) + " && set -o noclobber && mkdir -p " + shellSQ(path.Dir(marker)) + " && { > " + shellSQ(marker) + "; } 2>/dev/null) || exit 0\n" + patch.script
		req := ExecRequest{User: "root", Cmd: []string{"/bin/sh", "-c", script}}
		if patch.input != "" {
			req.Stdin = strings.NewReader(patch.input)
		}
		res, err := execer.Exec(ctx, req)
		if err != nil {
			return fmt.Errorf("%s: %w", patch.name, err)
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("%s: exit %d", patch.name, res.ExitCode)
		}
	}
	return nil
}

// EnvFromList decodes the inspected container environment, as upstream's
// envListToObj does. PID 1 can add variables that are not container settings.
func EnvFromList(values []string) map[string]string {
	env := make(map[string]string, len(values))
	for _, pair := range values {
		if key, value, ok := strings.Cut(pair, "="); ok {
			env[key] = value
		}
	}
	return env
}
