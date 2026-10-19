// Package cli composes the devcontainer libraries into the dcb command surface.
// Run writes one JSON result line except for help and exec.
// Help is plain text on stdout. Exec passes child streams through unchanged.
// Diagnostics and other build/lifecycle output use stderr.
// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
)

type application struct {
	version                string
	stdin                  io.Reader
	stderr                 io.Writer
	stdout                 io.Writer
	helpShown, execStarted bool
	outputErr              error
	logFormat, logLevel    string
	result                 map[string]any
	running                bool
	logs                   *logWriter
}

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string                { return e.err.Error() }
func (e *exitError) Unwrap() error                { return e.err }
func usageError(format string, args ...any) error { return &exitError{2, fmt.Errorf(format, args...)} }

// errVersionShown stops a command after --version produced the result.
var errVersionShown = errors.New("version shown")

// Run executes one invocation. Exit codes: 0 success, 1 operation failure,
// 2 invalid invocation. Exec returns the container command's nonzero exit code.
// Help is plain text. Exec emits no result after starting the child; operation
// failures before the child use stderr and exit 1. Invalid invocations use JSON.
// Embedded skills are carried in the JSON result as strings.
func Run(ctx context.Context, args []string, version string, stdin io.Reader, stdout, stderr io.Writer) int {
	a := &application{version: version, stdin: stdin, stderr: stderr, stdout: stdout, result: map[string]any{}}
	root := a.command()
	root.SetArgs(args)
	cmd, err := root.ExecuteContextC(ctx)
	if errors.Is(err, errVersionShown) {
		err = nil
	}
	if a.logs != nil {
		_ = a.logs.Flush()
	}
	code := 0
	if err != nil {
		code = 1
		if !a.running {
			code = 2
		}
		var ee *exitError
		if errors.As(err, &ee) {
			code = ee.code
		}
		a.result["outcome"] = "error"
		a.result["message"] = err.Error()
		a.result["description"] = "dcb could not complete the requested command"
		var conflict *dockercompose.ProjectConflictError
		if errors.As(err, &conflict) {
			a.result["composeProjectName"] = conflict.Project
			a.result["projectConflicts"] = conflict.Conflicts
			a.diagnostic("error", conflict.Error())
		}
	} else {
		a.result["outcome"] = "success"
	}
	if a.outputErr != nil {
		return 1
	}
	if a.helpShown || a.execStarted {
		return code
	}
	if cmd != nil && cmd.Name() == "exec" && code == 1 {
		// Operation failures are errors, even when optional diagnostics are off.
		if a.logs != nil {
			_ = a.logs.diagnostic("error", err.Error())
		}
		return code
	}
	if err := json.NewEncoder(stdout).Encode(a.result); err != nil {
		return 1
	}
	return code
}

func (a *application) command() *cobra.Command {
	var showVersion bool
	printVersion := func(_ *cobra.Command, _ []string) error { a.result["version"] = a.version; return nil }
	root := &cobra.Command{
		Use: "dcb", Short: "Build and manage development containers",
		Long:         "Build and manage development containers through the Docker Engine API.\n\nInvocations except help and exec write one JSON result line to stdout. Help is plain text.\nExec passes child stdout/stderr through unchanged without a result line; other logs and command output use stderr.\nExit codes: 0 success, 1 operation failure, 2 invalid invocation; exec propagates the command exit code.\nUse -- before a container command's own flags.",
		Example:      "  dcb up --workspace-folder .\n  dcb build --workspace-folder . --image-name example:dev\n  dcb exec --workspace-folder . -- sh -c 'id && pwd'\n  dcb read-configuration --workspace-folder .\n  dcb skills install --dir .agents/skills",
		SilenceUsage: true, SilenceErrors: true,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	// Upstream accepts --version on every command, in place of the command.
	root.PersistentFlags().BoolVar(&showVersion, "version", false, "Print the dcb version in place of the command (same result as dcb version)")
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		a.helpShown = true
		help := cmd.Long
		if help == "" {
			help = cmd.Short
		}
		help += "\n\n" + cmd.UsageString()
		if cmd.Parent() == nil {
			help += "\nCommon configuration flags (on workspace commands):\n  --workspace-folder string  Local workspace folder (default .)\n  --config string            Explicit devcontainer.json path\n  --override-config string   Alternate content, with original relative paths\n"
		}
		if _, err := io.WriteString(a.stdout, help); err != nil {
			a.outputErr = err
		}
	})
	root.PersistentFlags().StringVar(&a.logFormat, "log-format", "text", "Diagnostic format: text or json")
	root.PersistentFlags().StringVar(&a.logLevel, "log-level", "info", "Diagnostic level: trace, debug, info, warn, error, off")
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if showVersion {
			_ = printVersion(cmd, args)
			return errVersionShown
		}
		if a.logFormat != "text" && a.logFormat != "json" {
			return usageError("--log-format must be text or json")
		}
		switch a.logLevel {
		case "trace", "debug", "info", "warn", "error", "off":
		default:
			return usageError("invalid --log-level %q", a.logLevel)
		}
		a.logs = &logWriter{dst: a.stderr, json: a.logFormat == "json"}
		return nil
	}
	root.AddCommand(a.buildCommand(false), a.buildCommand(true), a.upCommand(), a.execCommand(), a.readCommand(), a.lifecycleCommand("run-user-commands"), a.lifecycleCommand("set-up"), a.manageCommand(false), a.manageCommand(true), a.featuresCommand(), a.serverCommand(), a.skillsCommand())
	root.AddCommand(&cobra.Command{Use: "version", Short: "Print the dcb version", Args: cobra.NoArgs, RunE: printVersion})
	wrapActions(root, a)
	versionBeforeArgs(root, &showVersion)
	return root
}

// logf adapts the libraries' printf logger, including their warning prefix.
func (a *application) logf(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	level := "info"
	if strings.HasPrefix(text, "warning:") {
		level = "warn"
	}
	a.diagnostic(level, text)
}

func (a *application) diagnostic(level, text string) {
	ranks := map[string]int{"trace": 0, "debug": 1, "info": 2, "warn": 3, "error": 4, "off": 5}
	if a.logs != nil && ranks[level] >= ranks[a.logLevel] {
		_ = a.logs.diagnostic(level, text)
	}
}

// versionBeforeArgs lets --version answer before positional argument checks,
// which Cobra runs ahead of pre-run hooks: upstream exec --version prints the
// version without a command to run.
func versionBeforeArgs(cmd *cobra.Command, showVersion *bool) {
	validate := cmd.Args
	cmd.Args = func(c *cobra.Command, args []string) error {
		if *showVersion || validate == nil {
			return nil
		}
		return validate(c, args)
	}
	for _, child := range cmd.Commands() {
		versionBeforeArgs(child, showVersion)
	}
}

// Cobra validates required flags after pre-run hooks. Mark operation errors only
// once the action begins so parser/argument/required-flag errors remain exit 2.
func wrapActions(cmd *cobra.Command, a *application) {
	if cmd.RunE != nil {
		action := cmd.RunE
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			a.running = true
			a.diagnostic("debug", "running "+cmd.CommandPath())
			a.diagnostic("trace", "arguments: "+fmt.Sprint(args))
			return action(cmd, args)
		}
	}
	for _, child := range cmd.Commands() {
		wrapActions(child, a)
	}
}
