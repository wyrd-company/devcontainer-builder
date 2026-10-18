// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	hostdocker "github.com/wyrd-company/devcontainer-builder/pkg/host/docker"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/runner"
)

func (a *application) execCommand() *cobra.Command {
	var cfg configOptions
	var id, user, workdir string
	var remoteEnv []string
	var tty bool
	cmd := &cobra.Command{Use: "exec [flags] -- <command> [args...]", Short: "Run a command in the devcontainer; pass through its stdout and stderr", Args: cobra.MinimumNArgs(1)}
	cfg.flags(cmd)
	cmd.Flags().BoolVar(&tty, "tty", false, "Allocate a pseudo-terminal for the container command")
	cmd.Flags().StringVar(&id, "container-id", "", "Target an existing container by ID")
	cmd.Flags().StringVar(&user, "user", "", "Exec user (default: remoteUser or container user)")
	cmd.Flags().StringVar(&workdir, "remote-workspace-folder", "", "Working directory in the container")
	cmd.Flags().StringArrayVar(&remoteEnv, "remote-env", nil, "Additional remote environment KEY=VALUE (repeat)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("container-id") && id == "" {
			return usageError("--container-id must not be empty")
		}
		extra, err := parsePairs(remoteEnv)
		if err != nil {
			return err
		}
		dc, workspace, err := cfg.optionalLoad()
		if err != nil {
			return err
		}
		cli, err := newDockerClient()
		if err != nil {
			return err
		}
		defer cli.Close()
		containerID, err := a.findContainer(cmd.Context(), cli, dc, workspace, id)
		if err != nil {
			return err
		}
		info, err := cli.ContainerInspect(cmd.Context(), containerID)
		if err != nil {
			return err
		}
		labels := info.Config.Labels
		if dc.Path == "" && labels[dockercompose.LabelProject] != "" {
			dc.Service = labels[dockercompose.LabelService]
		}
		dc = effectiveContainerConfig(dc, labels)
		var env map[string]string
		if referencesContainerEnv(dc.UpdateRemoteEnv) {
			env, err = runner.DetectContainerEnv(cmd.Context(), a.execer(cli, dc, containerID))
			if err != nil {
				return fmt.Errorf("detect container env: %w", err)
			}
		}
		execEnv := resolveRemoteEnv(dc.UpdateRemoteEnv, substitutionContext(dc, workspace, labels), env)
		for k, v := range extra {
			execEnv[k] = v
		}
		if user == "" {
			user = dc.RemoteUser
		}
		wd := workdir
		if wd == "" {
			wd = containerWorkspace(dc, workspace)
			if dc.WorkspaceFolder == "" && info.Config.WorkingDir != "" {
				wd = info.Config.WorkingDir
			}
		}
		a.execStarted = true
		res, err := a.execute(cmd.Context(), cli, dc, hostdocker.ExecRequest{ContainerID: containerID, TTY: tty, Cmd: args, User: user, WorkDir: wd, Env: execEnv, Stdin: a.stdin, Stdout: a.stdout, Stderr: a.stderr})
		if err != nil {
			a.execStarted = false
			return err
		}
		if res.ExitCode != 0 {
			return &exitError{res.ExitCode, fmt.Errorf("container command exited with code %d", res.ExitCode)}
		}
		return nil
	}
	return cmd
}

// Container environment probing is needed only for values with this placeholder.
// Non-root containers may not permit reading PID 1's environment even as root.
func referencesContainerEnv(env map[string]any) bool {
	for _, value := range env {
		if strings.Contains(fmt.Sprint(value), "${containerEnv:") {
			return true
		}
	}
	return false
}
