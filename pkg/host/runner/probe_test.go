// relationships:
//
//	references: [probe, env]
package runner

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type probeExecer func(context.Context, ExecRequest) (*ExecResult, error)

func (f probeExecer) Exec(ctx context.Context, r ExecRequest) (*ExecResult, error) { return f(ctx, r) }
func markedProbe(r ExecRequest, body string) *ExecResult {
	command := r.Cmd[len(r.Cmd)-1]
	mark := strings.Fields(command)[2]
	mark = strings.TrimSuffix(mark, ";")
	return &ExecResult{Stdout: []byte("startup noise\n" + mark + body + mark + "trailing noise")}
}

func TestUserEnvProbeModes(t *testing.T) {
	for _, tc := range []struct{ mode, flag string }{{"none", ""}, {"loginInteractiveShell", "-lic"}, {"interactiveShell", "-ic"}, {"loginShell", "-lc"}, {"", "-lic"}} {
		t.Run(tc.mode, func(t *testing.T) {
			calls := 0
			f := probeExecer(func(_ context.Context, r ExecRequest) (*ExecResult, error) {
				calls++
				if r.User != "sample" {
					t.Errorf("probe user = %q", r.User)
				}
				if calls == 1 {
					return &ExecResult{Stdout: []byte("/bin/ash\n1000\n")}, nil
				}
				if !reflect.DeepEqual(r.Cmd[:2], []string{"/bin/ash", tc.flag}) {
					t.Errorf("argv: %q", r.Cmd)
				}
				return markedProbe(r, "SAMPLE=one=two\x00PWD=/wrong\x00PATH=/bin:/usr/bin\x00"), nil
			})
			got := ProbeUserEnv(context.Background(), f, tc.mode, "", "sample", map[string]string{"PATH": "/bin:/opt/bin:/sbin"}, nil)
			if tc.mode == "none" {
				if calls != 0 || len(got) != 0 {
					t.Fatal("none ran a probe")
				}
				return
			}
			want := map[string]string{"SAMPLE": "one=two", "PATH": "/bin:/opt/bin:/usr/bin"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %v want %v", got, want)
			}
		})
	}
}

func TestUserEnvProbeShellAndConfiguration(t *testing.T) {
	for _, tc := range []struct {
		shell, passwd, uid, user, mode string
		prefix                         []string
	}{
		{"/bin/bash", "/bin/ash", "1000", "sample", "loginShell", []string{"/bin/bash", "-lc"}},
		{"", "/bin/ash", "0", "0:0", "interactiveShell", []string{"/bin/ash", "-ic"}},
		{"", "", "1000", "sample", "loginInteractiveShell", []string{"/bin/sh", "-lic"}},
		{"/usr/bin/pwsh", "/bin/sh", "1000", "sample", "loginShell", []string{"/usr/bin/pwsh", "-Login", "-Command"}},
		{"/usr/bin/pwsh-preview", "/bin/sh", "1000", "sample", "interactiveShell", []string{"/usr/bin/pwsh-preview", "-Command"}},
	} {
		t.Run(tc.shell+tc.passwd+tc.mode, func(t *testing.T) {
			calls := 0
			f := probeExecer(func(_ context.Context, r ExecRequest) (*ExecResult, error) {
				calls++
				if calls == 1 {
					return &ExecResult{Stdout: []byte(tc.passwd + "\n" + tc.uid + "\n")}, nil
				}
				if !reflect.DeepEqual(r.Cmd[:len(r.Cmd)-1], tc.prefix) {
					t.Errorf("argv: %q", r.Cmd)
				}
				return markedProbe(r, "SAMPLE=value\x00"), nil
			})
			got := ProbeUserEnv(context.Background(), f, tc.mode, "none", tc.user, map[string]string{"SHELL": tc.shell}, nil)
			if got["SAMPLE"] != "value" {
				t.Fatal(got)
			}
		})
	}
}

func TestUserEnvProbeFallback(t *testing.T) {
	for _, missing := range []string{"no markers", "empty markers"} {
		t.Run(missing, func(t *testing.T) {
			calls := 0
			f := probeExecer(func(_ context.Context, r ExecRequest) (*ExecResult, error) {
				calls++
				if calls == 1 {
					return &ExecResult{}, nil
				}
				if calls == 2 {
					if !strings.Contains(r.Cmd[len(r.Cmd)-1], "cat /proc/self/environ") {
						t.Error(r.Cmd)
					}
					if missing == "no markers" {
						return &ExecResult{Stdout: []byte("noise")}, nil
					}
					return markedProbe(r, ""), nil
				}
				if !strings.Contains(r.Cmd[len(r.Cmd)-1], "printenv") {
					t.Error(r.Cmd)
				}
				return markedProbe(r, "SAMPLE=fallback\nPWD=/wrong\n"), nil
			})
			got := ProbeUserEnv(context.Background(), f, "", "loginShell", "root", nil, nil)
			if calls != 3 || !reflect.DeepEqual(got, map[string]string{"SAMPLE": "fallback"}) {
				t.Fatalf("calls=%d env=%v", calls, got)
			}
		})
	}
}

func TestUserEnvProbeTimeout(t *testing.T) {
	calls := 0
	released := make(chan struct{})
	f := probeExecer(func(ctx context.Context, r ExecRequest) (*ExecResult, error) {
		calls++
		if calls == 1 {
			return &ExecResult{}, nil
		}
		<-ctx.Done()
		close(released)
		return nil, ctx.Err()
	})
	var logs []string
	start := time.Now()
	got := ProbeUserEnv(context.Background(), f, "", "loginShell", "root", nil, func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) })
	if elapsed := time.Since(start); elapsed < 10*time.Second || elapsed > 12*time.Second {
		t.Fatalf("timeout elapsed=%v", elapsed)
	}
	if got == nil || len(got) != 0 || calls != 2 {
		t.Fatalf("timeout fell back: calls=%d env=%v", calls, got)
	}
	select {
	case <-released:
	default:
		t.Fatal("probe goroutine did not return")
	}
	want := []string{"userEnvProbe is taking longer than 2 seconds.", "userEnvProbe is taking longer than 10 seconds. Avoid waiting for user input in your shell's startup scripts. Continuing."}
	if !reflect.DeepEqual(logs, want) {
		t.Fatalf("logs=%q", logs)
	}
}

func TestMergeProbePaths(t *testing.T) {
	for _, tc := range []struct {
		shell, container string
		root             bool
		want             string
	}{
		{"/bin:/usr/bin", "/opt:/bin:/sbin:/sbin/tools:/usr/bin:/end", false, "/opt:/bin:/usr/bin:/end"},
		{"/bin:/usr/bin", "/opt:/bin:/sbin:/sbin/tools:/usr/bin:/end", true, "/opt:/bin:/sbin:/sbin/tools:/usr/bin:/end"},
		{"/sbin:/bin:/bin:", "/bin::/new", false, "/sbin:/bin:/bin::/new"},
	} {
		if got := mergePaths(tc.shell, tc.container, tc.root); got != tc.want {
			t.Errorf("mergePaths = %q want %q", got, tc.want)
		}
	}
}

func TestUserEnvProbeShellFailureDoesNotFallback(t *testing.T) {
	calls := 0
	f := probeExecer(func(context.Context, ExecRequest) (*ExecResult, error) {
		calls++
		if calls == 1 {
			return &ExecResult{}, nil
		}
		return &ExecResult{ExitCode: 23}, nil
	})
	got := ProbeUserEnv(context.Background(), f, "loginShell", "", "root", nil, nil)
	if calls != 2 || len(got) != 0 {
		t.Fatalf("failed shell retried: calls=%d env=%v", calls, got)
	}
}
