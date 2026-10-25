// relationships:
//
//	references: [probe, probe_test, ../../../reference/cli/src/spec-common/injectHeadless]
package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Run cache IO against a real shell and filesystem, while controlling only
// passwd discovery and the interactive shell's environment output.
func TestProbeSessionCache(t *testing.T) {
	for _, mode := range []string{"loginShell", "interactiveShell", "loginInteractiveShell"} {
		t.Run(mode, func(t *testing.T) {
			folder := filepath.Join(t.TempDir(), "folder with ' quote")
			file := filepath.Join(folder, "env-"+mode+".json")
			probes := 0
			f := probeExecer(func(ctx context.Context, r ExecRequest) (*ExecResult, error) {
				if r.User != "sample" {
					t.Fatalf("user = %q", r.User)
				}
				if strings.Contains(r.Cmd[len(r.Cmd)-1], "id -u") {
					return &ExecResult{Stdout: []byte("/bin/sh\n1000\n")}, nil
				}
				if strings.Contains(r.Cmd[len(r.Cmd)-1], "echo -n") {
					probes++
					return markedProbe(r, "SAMPLE=line one\nline two\x00PWD=/excluded\x00"), nil
				}
				c := exec.CommandContext(ctx, r.Cmd[0], r.Cmd[1:]...)
				c.Stdin = r.Stdin
				out, err := c.Output()
				result := &ExecResult{Stdout: out}
				if e, ok := err.(*exec.ExitError); ok {
					result.ExitCode = e.ExitCode()
					return result, nil
				}
				return result, err
			})
			options := ProbeOptions{ContainerSessionDataFolder: folder}
			want := map[string]string{"SAMPLE": "line one\nline two"}
			for i := 0; i < 2; i++ {
				if got := ProbeUserEnv(context.Background(), f, mode, "none", "sample", nil, nil, options); !reflect.DeepEqual(got, want) {
					t.Fatalf("run %d: %v", i, got)
				}
			}
			if probes != 1 {
				t.Fatalf("probes = %d", probes)
			}
			data, err := os.ReadFile(file)
			if err != nil || !strings.Contains(string(data), `"SAMPLE"`) {
				t.Fatalf("cache = %s, %v", data, err)
			}
			if err := os.WriteFile(file, []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
			if got := ProbeUserEnv(context.Background(), f, mode, "", "sample", nil, nil, options); len(got) != 0 || probes != 1 {
				t.Fatalf("empty cache missed: %v, %d", got, probes)
			}
			if err := os.WriteFile(file, []byte("invalid json"), 0600); err != nil {
				t.Fatal(err)
			}
			var logs []string
			got := ProbeUserEnv(context.Background(), f, mode, "", "sample", nil, func(s string, a ...any) { logs = append(logs, fmt.Sprintf(s, a...)) }, options)
			if !reflect.DeepEqual(got, want) || probes != 2 || len(logs) != 1 || !strings.Contains(logs[0], "Failed to read/parse") {
				t.Fatalf("invalid cache recovery: %v %d %v", got, probes, logs)
			}
			// none must not touch an existing cache or execute anything.
			ProbeUserEnv(context.Background(), probeExecer(func(context.Context, ExecRequest) (*ExecResult, error) { t.Fatal("none executed"); return nil, nil }), "none", mode, "sample", nil, nil, options)
		})
	}
}

func TestProbeCacheFailuresAreNonFatal(t *testing.T) {
	for _, readError := range []bool{false, true} {
		t.Run(fmt.Sprint(readError), func(t *testing.T) {
			var logs []string
			writes := 0
			f := probeExecer(func(_ context.Context, r ExecRequest) (*ExecResult, error) {
				command := r.Cmd[len(r.Cmd)-1]
				switch {
				case strings.HasPrefix(command, "test -f"):
					if readError {
						return nil, fmt.Errorf("read unavailable")
					}
					return &ExecResult{ExitCode: 1}, nil
				case strings.Contains(command, "id -u"):
					return &ExecResult{Stdout: []byte("/bin/sh\n1000\n")}, nil
				case strings.HasPrefix(command, "mkdir -p"):
					writes++
					data, _ := io.ReadAll(r.Stdin)
					if !strings.Contains(string(data), `"SAMPLE": "value"`) {
						t.Fatalf("write = %s", data)
					}
					return &ExecResult{ExitCode: 1}, nil
				default:
					return markedProbe(r, "SAMPLE=value\x00"), nil
				}
			})
			got := ProbeUserEnv(context.Background(), f, "", "loginShell", "sample", nil, func(s string, a ...any) { logs = append(logs, fmt.Sprintf(s, a...)) }, ProbeOptions{ContainerSessionDataFolder: "/cache"})
			if got["SAMPLE"] != "value" || writes != 1 || !strings.Contains(strings.Join(logs, "\n"), "Failed to cache user env") {
				t.Fatalf("got %v writes %d logs %v", got, writes, logs)
			}
		})
	}
}

func TestProbeTimeoutCachesEmptyEnvironment(t *testing.T) {
	writes := 0
	f := probeExecer(func(ctx context.Context, r ExecRequest) (*ExecResult, error) {
		command := r.Cmd[len(r.Cmd)-1]
		switch {
		case strings.HasPrefix(command, "test -f"):
			return &ExecResult{ExitCode: 1}, nil
		case strings.Contains(command, "id -u"):
			return &ExecResult{}, nil
		case strings.HasPrefix(command, "mkdir -p"):
			writes++
			data, _ := io.ReadAll(r.Stdin)
			if string(data) != "{}\n" {
				t.Fatalf("cache = %s", data)
			}
			return &ExecResult{}, nil
		default:
			<-ctx.Done()
			return nil, ctx.Err()
		}
	})
	got := ProbeUserEnv(context.Background(), f, "loginShell", "", "sample", nil, nil, ProbeOptions{ContainerSessionDataFolder: "/cache", timeout: time.Millisecond})
	if got == nil || len(got) != 0 || writes != 1 {
		t.Fatalf("env=%v writes=%d", got, writes)
	}
}

func TestProbeShellFailureCachesEmptyEnvironment(t *testing.T) {
	for _, execError := range []bool{false, true} {
		t.Run(fmt.Sprint(execError), func(t *testing.T) {
			probes := 0
			var cache []byte
			f := probeExecer(func(_ context.Context, r ExecRequest) (*ExecResult, error) {
				command := r.Cmd[len(r.Cmd)-1]
				switch {
				case strings.HasPrefix(command, "test -f"):
					if cache == nil {
						return &ExecResult{ExitCode: 1}, nil
					}
					return &ExecResult{Stdout: cache}, nil
				case strings.Contains(command, "id -u"):
					return &ExecResult{Stdout: []byte("/bin/sh\n1000\n")}, nil
				case strings.HasPrefix(command, "mkdir -p"):
					cache, _ = io.ReadAll(r.Stdin)
					return &ExecResult{}, nil
				default:
					probes++
					if execError {
						return nil, fmt.Errorf("shell exec failed")
					}
					return &ExecResult{ExitCode: 1}, nil
				}
			})
			for i := 0; i < 2; i++ {
				got := ProbeUserEnv(context.Background(), f, "loginShell", "", "sample", nil, nil, ProbeOptions{ContainerSessionDataFolder: "/cache"})
				if got == nil || len(got) != 0 {
					t.Fatalf("run %d env=%v", i, got)
				}
			}
			if probes != 1 || string(cache) != "{}\n" {
				t.Fatalf("probes=%d cache=%s", probes, cache)
			}
		})
	}
}
