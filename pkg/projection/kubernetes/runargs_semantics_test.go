// relationships:
//
//	implements: schema-conformance
package kubernetes

import (
	"reflect"
	"strings"
	"testing"

	dockeropts "github.com/docker/cli/opts"
	oci "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/pflag"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestRunArgsNativeBooleans(t *testing.T) {
	for _, args := range [][]string{
		{"-it"}, {"--init=true", "--init=false", "-P", "-P=0"}, {"-it=false"}, {"--interactive=0", "--tty=TRUE"},
		{"--privileged", "--privileged=false", "--read-only=1"},
		{"--read-only=true", "--read-only=F"}, {"-dit", "-d=false"},
		{"--init=false", "-P=false", "--rm=FALSE", "--sig-proxy=0"},
		{"--tty=invalid"}, {"--rm=invalid"}, {"--interactive", "false"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fs := pflag.NewFlagSet("native", pflag.ContinueOnError)
			stdin := fs.BoolP("interactive", "i", false, "")
			tty := fs.BoolP("tty", "t", false, "")
			privileged := fs.Bool("privileged", false, "")
			readonly := fs.Bool("read-only", false, "")
			detach := fs.BoolP("detach", "d", false, "")
			fs.Bool("init", false, "")
			fs.BoolP("publish-all", "P", false, "")
			fs.Bool("rm", false, "")
			fs.Bool("sig-proxy", true, "")
			nativeErr := fs.Parse(args)
			if nativeErr == nil && len(fs.Args()) > 0 {
				nativeErr = errPositional{}
			}
			pj, err := Project(ProjectRequest{Spec: &spec.DevContainer{RunArgs: args}, Image: "alpine"})
			if nativeErr != nil {
				if err == nil {
					t.Fatalf("accepted invalid native argv %v", args)
				}
				return
			}
			if *detach {
				if err == nil {
					t.Fatal("missing inherited attachment conflict")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			c := pj.Pod.Spec.Containers[0]
			if c.Stdin != *stdin || c.TTY != *tty {
				t.Fatalf("stdin/tty %v/%v want %v/%v", c.Stdin, c.TTY, *stdin, *tty)
			}
			if c.SecurityContext != nil {
				if c.SecurityContext.Privileged != nil && *c.SecurityContext.Privileged != *privileged {
					t.Fatal("privileged mismatch")
				}
				if c.SecurityContext.ReadOnlyRootFilesystem != nil && *c.SecurityContext.ReadOnlyRootFilesystem != *readonly {
					t.Fatal("read-only mismatch")
				}
			} else if *privileged || *readonly {
				t.Fatal("missing security fields")
			}
		})
	}
}

type errPositional struct{}

func (errPositional) Error() string { return "positional" }

func TestRunArgsAttachedValuesAndErrors(t *testing.T) {
	pod := projectArgs(t, "-iteSAMPLE=value", "-u123:456", "-w/workspace", "-hhost", "-aSTDERR")
	c := pod.Spec.Containers[0]
	if !c.Stdin || !c.TTY || envAsMap(c.Env)["SAMPLE"] != "value" || *c.SecurityContext.RunAsUser != 123 || *c.SecurityContext.RunAsGroup != 456 || c.WorkingDir != "/workspace" || pod.Spec.Hostname != "host" {
		t.Fatalf("pod=%+v container=%+v", pod.Spec, c)
	}
	for _, args := range [][]string{{"-dt"}, {"--detach=true"}, {"-aunknown"}, {"-e"}, {"-e=bad"}, {"--init=true"}, {"-P=true"}, {"-itZ"}} {
		if err := projectArgsErr(t, args...); err == nil || !strings.Contains(err.Error(), "runArgs:") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestRunArgsNativeEnvironment(t *testing.T) {
	t.Setenv("SAMPLE_SET", "$(LITERAL):two words")
	t.Setenv("SAMPLE_EMPTY", "")
	for _, v := range []string{"SAMPLE_SET", "SAMPLE_EMPTY", "SAMPLE_SET=explicit", "SAMPLE_SET=", "=invalid"} {
		native, nativeErr := dockeropts.ValidateEnv(v)
		pj, err := Project(ProjectRequest{Spec: &spec.DevContainer{RunArgs: []string{"-e", v}}, Image: "alpine"})
		if nativeErr != nil {
			if err == nil {
				t.Fatal("empty key accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		k, value, _ := strings.Cut(native, "=")
		if got := envAsMap(pj.Pod.Spec.Containers[0].Env)[k]; got != strings.ReplaceAll(value, "$", "$$") {
			t.Fatalf("%s=%q native=%q", k, got, native)
		}
	}
	// Explicit caller environment is isolated from the process; the last key wins.
	pj, err := Project(ProjectRequest{Spec: &spec.DevContainer{ContainerEnv: map[string]any{"SAMPLE_SET": "config"}, RunArgs: []string{"-eSAMPLE_SET=first", "-eSAMPLE_SET", "-eSAMPLE_EMPTY"}}, Image: "alpine", LocalEnv: map[string]string{"SAMPLE_SET": "caller", "SAMPLE_EMPTY": ""}})
	if err != nil {
		t.Fatal(err)
	}
	if got := envAsMap(pj.Pod.Spec.Containers[0].Env); !reflect.DeepEqual(got, map[string]string{"SAMPLE_SET": "caller", "SAMPLE_EMPTY": ""}) {
		t.Fatal(got)
	}
}

func TestRunArgsUnsetEnvironment(t *testing.T) {
	dc := &spec.DevContainer{ContainerEnv: map[string]any{"SAMPLE_UNSET": "config"}, RunArgs: []string{"-eSAMPLE_UNSET"}}
	req := ProjectRequest{Spec: dc, Image: "alpine", LocalEnv: map[string]string{}, ImageConfig: &oci.ImageConfig{}}
	pj, err := Project(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := envAsMap(pj.Pod.Spec.Containers[0].Env)["SAMPLE_UNSET"]; ok {
		t.Fatal("unset value retained")
	}
	req.ImageConfig = nil
	if _, err := Project(req); err == nil || !strings.Contains(err.Error(), "ImageConfig is required") {
		t.Fatal(err)
	}
	req.ImageConfig = &oci.ImageConfig{Env: []string{"SAMPLE_UNSET=image"}}
	if _, err := Project(req); err == nil || !strings.Contains(err.Error(), "unsetting image environment") {
		t.Fatal(err)
	}
}

func TestRunArgsMergedFlagsFollowArguments(t *testing.T) {
	yes := true
	req := ProjectRequest{Spec: &spec.DevContainer{Privileged: &yes, CapAdd: []string{"CONFIG"}, SecurityOpt: []string{"seccomp=default"}, RunArgs: []string{"--privileged=false", "--cap-add=ARGUMENT", "--security-opt=seccomp=unconfined"}}, Image: "alpine", Features: []features.Resolved{{Metadata: features.Metadata{CapAdd: []string{"FEATURE"}}}}}
	pj, err := Project(req)
	if err != nil {
		t.Fatal(err)
	}
	sc := pj.Pod.Spec.Containers[0].SecurityContext
	if !*sc.Privileged || string(sc.SeccompProfile.Type) != "RuntimeDefault" || len(sc.Capabilities.Add) != 3 || strings.Join([]string{string(sc.Capabilities.Add[0]), string(sc.Capabilities.Add[1]), string(sc.Capabilities.Add[2])}, ",") != "ARGUMENT,FEATURE,CONFIG" {
		t.Fatalf("security=%+v caps=%v", sc, sc.Capabilities.Add)
	}
}

func TestRunArgsMergedInitError(t *testing.T) {
	yes := true
	for _, req := range []ProjectRequest{
		{Spec: &spec.DevContainer{Init: &yes, RunArgs: []string{"--init=false"}}, Image: "alpine"},
		{Spec: &spec.DevContainer{}, Image: "alpine", Features: []features.Resolved{{Metadata: features.Metadata{Init: &yes}}}},
	} {
		if _, err := Project(req); err == nil || !strings.Contains(err.Error(), "merged --init") {
			t.Fatal(err)
		}
	}
}

func TestRunArgsUnsetThenSetEnvironment(t *testing.T) {
	for _, image := range []*oci.ImageConfig{nil, {}, {Env: []string{"SAMPLE_ABSENT=image"}}} {
		for _, value := range []string{"final", ""} {
			req := ProjectRequest{Spec: &spec.DevContainer{RunArgs: []string{"-eSAMPLE_ABSENT", "-eSAMPLE_ABSENT=" + value}}, Image: "alpine", ImageConfig: image, LocalEnv: map[string]string{}}
			pj, err := Project(req)
			if err != nil {
				t.Fatal(err)
			}
			got := envAsMap(pj.Pod.Spec.Containers[0].Env)
			if actual, ok := got["SAMPLE_ABSENT"]; !ok || actual != value {
				t.Fatalf("env=%v want explicit %q", got, value)
			}
			// A final unset still needs inspection / no image-owned key.
			req.Spec.RunArgs = append(req.Spec.RunArgs, "-eSAMPLE_ABSENT")
			_, err = Project(req)
			if image == nil || len(image.Env) > 0 {
				if err == nil {
					t.Fatal("final unset accepted")
				}
			} else if err != nil {
				t.Fatal(err)
			}
		}
	}
}
