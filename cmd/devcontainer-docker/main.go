// Command devcontainer-docker brings a devcontainer up against a
// docker-compatible daemon. It's a thin UI on top of pkg/host/docker;
// services and operators that want the same behavior should embed the
// package directly rather than shell out to this binary.
//
// Subcommands:
//
//	up      Build the image (if needed), create + start a container,
//	        run lifecycle commands.
//	exec    Run a command inside the running devcontainer.
//	down    Stop and remove the devcontainer for a workspace.
//	inspect Print the discovered container's metadata.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	dockerclient "github.com/docker/docker/client"

	"github.com/wyrd-company/devcontainer-builder/pkg/builder"
	hostdocker "github.com/wyrd-company/devcontainer-builder/pkg/host/docker"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// Version is overridden at link time via -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var err error
	switch cmd {
	case "up":
		err = cmdUp(ctx, args)
	case "exec":
		err = cmdExec(ctx, args)
	case "down":
		err = cmdDown(ctx, args)
	case "inspect":
		err = cmdInspect(ctx, args)
	case "version", "--version", "-v":
		fmt.Printf("devcontainer-docker %s\n", Version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.SetFlags(0)
		log.Printf("error: %v", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `devcontainer-docker - run devcontainer-spec containers on docker

Usage:
  devcontainer-docker <command> [flags]

Commands:
  up       Build, create, start, and run lifecycle commands
  exec     Run a command in the running devcontainer
  down     Stop and remove the devcontainer
  inspect  Print devcontainer state
  version  Print version
  help     Show this help

Run "devcontainer-docker <command> -h" for command-specific flags.`)
}

func cmdUp(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	var (
		workspace = fs.String("workspace", ".", "workspace folder containing .devcontainer/")
		specPath  = fs.String("spec", "", "path to devcontainer.json (overrides workspace lookup)")
		image     = fs.String("image", "", "use this image instead of building (skips builder)")
		tag       = fs.String("tag", "", "tag for the built image (default: devcontainer-<basename>:latest)")
		skipBuild = fs.Bool("skip-build", false, "do not build the image; --image must be supplied or already present")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	absWorkspace, err := filepath.Abs(*workspace)
	if err != nil {
		return err
	}

	dc, err := loadSpec(*specPath, absWorkspace)
	if err != nil {
		return err
	}
	resolvedSpecPath := dc.Path

	resolvedImage := *image
	if !*skipBuild && resolvedImage == "" {
		t := *tag
		if t == "" {
			t = "devcontainer-" + sanitizeTag(filepath.Base(absWorkspace)) + ":latest"
		}
		log.Printf("building image %s", t)
		buildOpts := builder.Options{
			SpecPath:  resolvedSpecPath,
			Workspace: absWorkspace,
			Tags:      []string{t},
			Load:      true,
			Logger:    stderrLogger("build"),
			Stdout:    os.Stdout,
			Stderr:    os.Stderr,
			Progress:  "auto",
		}
		if _, err := builder.New().Build(ctx, buildOpts); err != nil {
			return fmt.Errorf("build: %w", err)
		}
		resolvedImage = t
	}
	if resolvedImage == "" {
		return fmt.Errorf("no image: pass --image or remove --skip-build")
	}

	cli, err := newDockerClient()
	if err != nil {
		return err
	}
	defer cli.Close()

	host := hostdocker.New(cli, hostdocker.Options{
		Logger: stderrLogger("docker"),
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})

	res, err := host.Up(ctx, hostdocker.UpRequest{
		Spec:           dc,
		SpecPath:       resolvedSpecPath,
		LocalWorkspace: absWorkspace,
		Image:          resolvedImage,
		LocalEnv:       envMap(),
	})
	if err != nil {
		return err
	}

	verb := "reused"
	if res.CreatedThisCall {
		verb = "created"
	} else if res.StartedThisCall {
		verb = "started"
	}
	fmt.Fprintf(os.Stderr, "container %s (%s)\n", res.ContainerID[:12], verb)
	if len(res.PhasesRun) > 0 {
		fmt.Fprintf(os.Stderr, "lifecycle phases run: %v\n", res.PhasesRun)
	}
	return nil
}

func cmdExec(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	var (
		workspace = fs.String("workspace", ".", "workspace folder")
		userFlag  = fs.String("u", "", "user (default: spec remoteUser)")
		workdir   = fs.String("w", "", "working directory (default: spec workspaceFolder)")
		ttyFlag   = fs.Bool("t", true, "allocate a pseudo-TTY")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cmdArgs := fs.Args()
	if len(cmdArgs) == 0 {
		return fmt.Errorf("exec: provide a command")
	}

	absWorkspace, err := filepath.Abs(*workspace)
	if err != nil {
		return err
	}

	cli, err := newDockerClient()
	if err != nil {
		return err
	}
	defer cli.Close()
	host := hostdocker.New(cli, hostdocker.Options{Logger: stderrLogger("docker")})

	info, err := host.Inspect(ctx, absWorkspace)
	if err != nil {
		return err
	}

	user := *userFlag
	wd := *workdir
	if user == "" || wd == "" {
		dc, _ := spec.LoadFromWorkspace(absWorkspace)
		if dc != nil {
			if user == "" {
				user = dc.RemoteUser
			}
			if wd == "" {
				wd = dc.WorkspaceFolder
			}
		}
	}
	if wd == "" {
		wd = info.WorkingDir
	}

	res, err := host.Exec(ctx, hostdocker.ExecRequest{
		ContainerID: info.ID,
		Cmd:         cmdArgs,
		User:        user,
		WorkDir:     wd,
		TTY:         *ttyFlag,
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
	})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		os.Exit(res.ExitCode)
	}
	return nil
}

func cmdDown(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("down", flag.ExitOnError)
	workspace := fs.String("workspace", ".", "workspace folder")
	if err := fs.Parse(args); err != nil {
		return err
	}
	absWorkspace, err := filepath.Abs(*workspace)
	if err != nil {
		return err
	}
	cli, err := newDockerClient()
	if err != nil {
		return err
	}
	defer cli.Close()
	host := hostdocker.New(cli, hostdocker.Options{Logger: stderrLogger("docker")})
	if err := host.Remove(ctx, absWorkspace); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "removed")
	return nil
}

func cmdInspect(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	workspace := fs.String("workspace", ".", "workspace folder")
	if err := fs.Parse(args); err != nil {
		return err
	}
	absWorkspace, err := filepath.Abs(*workspace)
	if err != nil {
		return err
	}
	cli, err := newDockerClient()
	if err != nil {
		return err
	}
	defer cli.Close()
	host := hostdocker.New(cli, hostdocker.Options{})
	info, err := host.Inspect(ctx, absWorkspace)
	if err != nil {
		return err
	}
	fmt.Printf("id:         %s\n", info.ID)
	fmt.Printf("image:      %s\n", info.Image)
	fmt.Printf("running:    %t\n", info.Running)
	fmt.Printf("createdAt:  %s\n", info.CreatedAt)
	fmt.Printf("startedAt:  %s\n", info.StartedAt)
	fmt.Printf("workdir:    %s\n", info.WorkingDir)
	if len(info.Labels) > 0 {
		fmt.Println("labels:")
		for k, v := range info.Labels {
			fmt.Printf("  %s=%s\n", k, v)
		}
	}
	return nil
}

func loadSpec(specPath, workspace string) (*spec.DevContainer, error) {
	if specPath != "" {
		return spec.Load(specPath)
	}
	return spec.LoadFromWorkspace(workspace)
}

// newDockerClient negotiates an API version against $DOCKER_HOST.
func newDockerClient() (*dockerclient.Client, error) {
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return cli, nil
}

// envMap collects the host environment as a string map for variable
// substitution.
func envMap() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		if eq := indexByte(kv, '='); eq >= 0 {
			out[kv[:eq]] = kv[eq+1:]
		}
	}
	return out
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// sanitizeTag turns a workspace basename into something docker accepts
// in an image reference (lowercase, [a-z0-9_-]).
func sanitizeTag(s string) string {
	s = strings.ToLower(s)
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "devcontainer"
	}
	return string(out)
}

func stderrLogger(prefix string) func(string, ...any) {
	return func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "[%s] ", prefix)
		fmt.Fprintf(os.Stderr, format, args...)
		fmt.Fprintln(os.Stderr)
	}
}
