//go:build docker_integration

// relationships:
//   references: [build_sessions, builder, ../buildkit/dockerfile, ../buildkit/gateway]

package builder

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/buildkit"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"golang.org/x/crypto/ssh/agent"
)

func TestBuildSessionsEngine(t *testing.T) {
	ctx := context.Background()
	cli := requireEngine(t, ctx)
	dir := t.TempDir()
	socket := filepath.Join(dir, "agent.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _ = agent.ServeAgent(agent.NewKeyring(), conn) }()
		}
	}()
	t.Setenv("SAMPLE_BUILD_VALUE", "sample-value")
	df := "FROM alpine\nRUN apk add --no-cache openssh-client\nRUN --mount=type=secret,id=sample,required=true --mount=type=ssh,id=sample,required=true test \"$(cat /run/secrets/sample)\" = sample-value && test -S \"$SSH_AUTH_SOCK\" && (ssh-add -l >/agent-result 2>&1; test $? = 1) && grep -q \"agent has no identities\" /agent-result\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(df), 0600); err != nil {
		t.Fatal(err)
	}
	dc := &spec.DevContainer{Path: filepath.Join(dir, "devcontainer.json"), Build: &spec.Build{Dockerfile: "Dockerfile", Options: []string{"--secret=id=sample,env=SAMPLE_BUILD_VALUE", "--ssh=sample=" + socket}}}
	for _, route := range []string{"standalone", "nested", "public"} {
		t.Run(route, func(t *testing.T) {
			opts := Options{Spec: dc, Workspace: dir, WorkDir: t.TempDir(), Docker: cli, Output: "type=cacheonly", NoCache: true, SkipFeatureAutoMapping: true, SkipImageMetaResolve: true, Stderr: io.Discard, Stdout: io.Discard}
			wire, err := solveOptions(opts, dir, dc)
			if err != nil {
				t.Fatal(err)
			}
			b := buildkit.DockerfileBuild{ContextDir: dir}
			switch route {
			case "standalone":
				_, err = buildkit.SolveDockerfile(ctx, b, wire)
			case "nested":
				_, err = buildkit.SolveDevcontainer(ctx, buildkit.BuildSpec{Upstream: &b, Graph: buildkit.GraphInputs{Spec: &spec.DevContainer{}}}, wire)
			case "public":
				_, err = New().Build(ctx, opts)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
	// Required mounts must fail when no session provider is supplied.
	_, err = buildkit.SolveDockerfile(ctx, buildkit.DockerfileBuild{ContextDir: dir}, buildkit.SolveOptions{Docker: cli, Output: "type=cacheonly", NoCache: true, Stderr: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("missing session error=%v", err)
	}
}
