// relationships:
//
//	references: [env, ../../../reference/cli/src/spec-common/injectHeadless]
package runner

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"
)

const DefaultUserEnvProbe = "loginInteractiveShell"

// ProbeOptions identifies whether root execution and system configuration
// changes are available. Docker and Compose provide both, as upstream's CLI.
type ProbeOptions struct {
	CanPatchSystemConfig bool
	// ContainerSessionDataFolder stores mode-specific probe results; empty disables caching.
	ContainerSessionDataFolder string
	diagnosticAfter            time.Duration
	timeout                    time.Duration
}

func ValidUserEnvProbe(mode string) bool {
	switch mode {
	case "none", "loginInteractiveShell", "interactiveShell", "loginShell":
		return true
	}
	return false
}

// ProbeUserEnv reads the user's shell environment inside the container. Configuration
// wins over the caller's default. Failures are non-fatal, as in the upstream CLI.
func ProbeUserEnv(ctx context.Context, execer Execer, configured, fallback, user string, containerEnv map[string]string, logf func(string, ...any), options ProbeOptions) map[string]string {
	if options.diagnosticAfter == 0 {
		options.diagnosticAfter = 2 * time.Second
	}
	if options.timeout == 0 {
		options.timeout = 10 * time.Second
	}
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
	if env := readProbeCache(ctx, execer, user, mode, options.ContainerSessionDataFolder, log); env != nil {
		return env
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
		diagnostic := time.NewTimer(options.diagnosticAfter)
		limit := time.NewTimer(options.timeout)
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
				env := map[string]string{}
				writeProbeCache(ctx, execer, user, mode, options.ContainerSessionDataFolder, env, log)
				return env
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
		if !options.CanPatchSystemConfig && env["PATH"] != "" && containerEnv["PATH"] != "" {
			env["PATH"] = mergePaths(env["PATH"], containerEnv["PATH"], root)
		}
		writeProbeCache(ctx, execer, user, mode, options.ContainerSessionDataFolder, env, log)
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

// Cache files follow the upstream mode-only key and contain the probe result,
// before remoteEnv or lifecycle secrets are layered onto it.
func readProbeCache(ctx context.Context, execer Execer, user, mode, folder string, log func(string, ...any)) map[string]string {
	if folder == "" {
		return nil
	}
	file := shellSQ(path.Join(folder, "env-"+mode+".json"))
	res, err := execer.Exec(ctx, ExecRequest{User: user, Cmd: []string{"/bin/sh", "-c", "test -f " + file + " && cat " + file}})
	if err != nil {
		log("Failed to read/parse user env cache: %v", err)
		return nil
	}
	if res.ExitCode != 0 {
		return nil
	}
	var env map[string]string
	if err := json.Unmarshal(res.Stdout, &env); err != nil {
		log("Failed to read/parse user env cache: %v", err)
		return nil
	}
	return env
}

func writeProbeCache(ctx context.Context, execer Execer, user, mode, folder string, env map[string]string, log func(string, ...any)) {
	if folder == "" {
		return
	}
	data, err := json.MarshalIndent(env, "", "\t")
	if err != nil {
		log("Failed to cache user env: %v", err)
		return
	}
	file := path.Join(folder, "env-"+mode+".json")
	res, err := execer.Exec(ctx, ExecRequest{User: user, Cmd: []string{"/bin/sh", "-c", "mkdir -p " + shellSQ(path.Dir(file)) + " && cat > " + shellSQ(file)}, Stdin: strings.NewReader(string(data) + "\n")})
	if err == nil && res.ExitCode != 0 {
		err = fmt.Errorf("cache write exited with code %d", res.ExitCode)
	}
	if err != nil {
		log("Failed to cache user env: %v", err)
	}
}
