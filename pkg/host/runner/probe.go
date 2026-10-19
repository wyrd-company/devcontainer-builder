// relationships:
//
//	references: [env, ../../../reference/cli/src/spec-common/injectHeadless]
package runner

import (
	"context"
	"crypto/rand"
	"fmt"
	"path"
	"strings"
	"time"
)

const DefaultUserEnvProbe = "loginInteractiveShell"

func ValidUserEnvProbe(mode string) bool {
	switch mode {
	case "none", "loginInteractiveShell", "interactiveShell", "loginShell":
		return true
	}
	return false
}

// ProbeUserEnv reads the user's shell environment inside the container. Configuration
// wins over the caller's default. Failures are non-fatal, as in the upstream CLI.
func ProbeUserEnv(ctx context.Context, execer Execer, configured, fallback, user string, containerEnv map[string]string, logf func(string, ...any)) map[string]string {
	mode := configured
	if mode == "" {
		mode = fallback
	}
	if mode == "" {
		mode = DefaultUserEnvProbe
	}
	if mode == "none" {
		return nil
	}
	log := func(format string, args ...any) {
		if logf != nil {
			logf(format, args...)
		}
	}
	// Run as the selected user so id resolves the default user and uid:gid forms.
	res, err := execer.Exec(ctx, ExecRequest{User: user, Cmd: []string{"/bin/sh", "-c", `uid=$(id -u); (command -v getent >/dev/null 2>&1 && getent passwd "$uid" || grep -E "^[^:]*:[^:]*:$uid:" /etc/passwd) | cut -d: -f7; printf '%s\n' "$uid"`}})
	shell, root := containerEnv["SHELL"], user == "root" || user == "0"
	if err == nil && res.ExitCode == 0 {
		fields := strings.Split(strings.TrimSpace(string(res.Stdout)), "\n")
		root = fields[len(fields)-1] == "0"
		if shell == "" && len(fields) > 1 {
			shell = fields[0]
		}
	}
	if shell == "" {
		shell = "/bin/sh"
	}
	args := []string{"-c"}
	switch mode {
	case "loginInteractiveShell":
		args[0] = "-lic"
	case "loginShell":
		args[0] = "-lc"
	case "interactiveShell":
		args[0] = "-ic"
	}
	if name := path.Base(shell); name == "pwsh" || name == "pwsh-preview" {
		args = []string{"-Command"}
		if mode == "loginInteractiveShell" || mode == "loginShell" {
			args = []string{"-Login", "-Command"}
		}
	}
	for _, probe := range []struct{ cmd, separator string }{{"cat /proc/self/environ", "\x00"}, {"printenv", "\n"}} {
		mark := fmt.Sprintf("%x", makeProbeMark())
		command := "echo -n " + mark + "; " + probe.cmd + "; echo -n " + mark
		argv := append(append([]string{shell}, args...), command)
		probeCtx, cancel := context.WithCancel(ctx)
		type result struct {
			res *ExecResult
			err error
		}
		done := make(chan result, 1)
		go func() {
			res, err := execer.Exec(probeCtx, ExecRequest{User: user, Cmd: argv})
			done <- result{res, err}
		}()
		diagnostic := time.NewTimer(2 * time.Second)
		limit := time.NewTimer(10 * time.Second)
		var resultValue result
	wait:
		for {
			select {
			case resultValue = <-done:
				break wait
			case <-diagnostic.C:
				log("userEnvProbe is taking longer than 2 seconds.")
			case <-limit.C:
				cancel()
				<-done
				diagnostic.Stop()
				log("userEnvProbe is taking longer than 10 seconds. Avoid waiting for user input in your shell's startup scripts. Continuing.")
				return map[string]string{}
			case <-ctx.Done():
				cancel()
				<-done
				diagnostic.Stop()
				limit.Stop()
				return nil
			}
		}
		cancel()
		diagnostic.Stop()
		limit.Stop()
		if resultValue.err == nil && resultValue.res.ExitCode != 0 {
			resultValue.err = fmt.Errorf("shell exited with code %d", resultValue.res.ExitCode)
		}
		if resultValue.err != nil {
			log("Error reading shell environment: %v", resultValue.err)
			return nil
		}
		raw := string(resultValue.res.Stdout)
		start := strings.Index(raw, mark)
		if start < 0 {
			continue
		}
		raw = raw[start+len(mark):]
		end := strings.LastIndex(raw, mark)
		if end <= 0 {
			continue
		}
		env := map[string]string{}
		for _, pair := range strings.Split(raw[:end], probe.separator) {
			if key, value, ok := strings.Cut(pair, "="); ok {
				env[key] = value
			}
		}
		delete(env, "PWD")
		if env["PATH"] != "" && containerEnv["PATH"] != "" {
			env["PATH"] = mergePaths(env["PATH"], containerEnv["PATH"], root)
		}
		return env
	}
	return nil
}

func makeProbeMark() []byte {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func mergePaths(shellPath, containerPath string, root bool) string {
	result, insertAt := strings.Split(shellPath, ":"), 0
	for _, entry := range strings.Split(containerPath, ":") {
		index := -1
		for i, existing := range result {
			if existing == entry {
				index = i
				break
			}
		}
		if index >= 0 {
			insertAt = index + 1
			continue
		}
		if root || !(strings.HasSuffix(entry, "/sbin") || strings.Contains(entry, "/sbin/")) {
			result = append(result, "")
			copy(result[insertAt+1:], result[insertAt:])
			result[insertAt] = entry
			insertAt++
		}
	}
	return strings.Join(result, ":")
}
