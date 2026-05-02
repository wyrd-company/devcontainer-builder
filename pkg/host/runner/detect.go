package runner

import (
	"context"
	"fmt"
	"strings"
)

// DetectHome runs `getent passwd <user>` (or returns "/root" for
// root) to find a user's home directory inside the container. The
// home directory is the basis for the marker file paths under
// ~/.devcontainer.
//
// Falls back to "/home/<user>" when getent isn't available or the
// user isn't in /etc/passwd — matching the behavior in
// pkg/host/docker so containers built from minimal base images
// (alpine, distroless variants) still get marker files at a
// predictable path.
func DetectHome(ctx context.Context, execer Execer, user string) (string, error) {
	if user == "" || user == "root" {
		return "/root", nil
	}
	res, err := execer.Exec(ctx, ExecRequest{
		Cmd:  []string{"/bin/sh", "-c", fmt.Sprintf("getent passwd %s | cut -d: -f6", shellSQ(user))},
		User: "root",
	})
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(strings.TrimRight(string(res.Stdout), "\n"))
	if res.ExitCode != 0 || out == "" {
		return "/home/" + user, nil
	}
	return out, nil
}

// DetectContainerEnv reads /proc/1/environ inside the container to
// find the effective environment available to lifecycle commands.
// The result is suitable for ${containerEnv:VAR} substitution via
// pkg/substitute.ContainerSubstitute.
//
// Always reads as root since /proc/<pid>/environ is mode 0400.
func DetectContainerEnv(ctx context.Context, execer Execer) (map[string]string, error) {
	res, err := execer.Exec(ctx, ExecRequest{
		Cmd:  []string{"/bin/sh", "-c", "cat /proc/1/environ"},
		User: "root",
	})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("/proc/1/environ: exit %d", res.ExitCode)
	}
	env := map[string]string{}
	for _, kv := range strings.Split(string(res.Stdout), "\x00") {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		env[kv[:eq]] = kv[eq+1:]
	}
	return env, nil
}
