// Package cli composes the devcontainer libraries into the dcb command surface.
// Run returns an exit code and writes exactly one JSON result line to stdout.
// Diagnostics and container command output are sent to stderr.
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

	"github.com/spf13/cobra"
)

type application struct {
	version             string
	stdin               io.Reader
	stderr              io.Writer
	logFormat, logLevel string
	result              map[string]any
	running             bool
	logs                *logWriter
}

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string                { return e.err.Error() }
func (e *exitError) Unwrap() error                { return e.err }
func usageError(format string, args ...any) error { return &exitError{2, fmt.Errorf(format, args...)} }

// Run executes one invocation. Exit codes: 0 success, 1 operation failure,
// 2 invalid invocation. Exec returns the container command's nonzero exit code.
// Help and embedded skills are carried in the JSON result as strings.
func Run(ctx context.Context, args []string, version string, stdin io.Reader, stdout, stderr io.Writer) int {
	a := &application{version: version, stdin: stdin, stderr: stderr, result: map[string]any{}}
	root := a.command()
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
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
	} else {
		a.result["outcome"] = "success"
	}
	if err := json.NewEncoder(stdout).Encode(a.result); err != nil {
		return 1
	}
	return code
}

func (a *application) command() *cobra.Command {
	root := &cobra.Command{
		Use: "dcb", Short: "Build and manage development containers",
		Long:         "Build and manage development containers through the Docker Engine API.\n\nEvery invocation writes one JSON result line to stdout; logs and command output use stderr.\nExit codes: 0 success, 1 operation failure, 2 invalid invocation; exec propagates the command exit code.\nUse -- before a container command's own flags.",
		Example:      "  dcb up --workspace-folder .\n  dcb build --workspace-folder . --image-name example:dev\n  dcb exec --workspace-folder . -- sh -c 'id && pwd'\n  dcb read-configuration --workspace-folder .\n  dcb skills install --dir .agents/skills",
		SilenceUsage: true, SilenceErrors: true,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		a.result["help"] = cmd.Long + "\n\n" + cmd.UsageString()
		if cmd.Parent() == nil {
			a.result["help"] = a.result["help"].(string) + "\nCommon configuration flags (on workspace commands):\n  --workspace-folder string  Local workspace folder (default .)\n  --config string            Explicit devcontainer.json path\n  --override-config string   Alternate content, with original relative paths\n"
		}
	})
	root.PersistentFlags().StringVar(&a.logFormat, "log-format", "text", "Diagnostic format: text or json")
	root.PersistentFlags().StringVar(&a.logLevel, "log-level", "info", "Diagnostic level: trace, debug, info, warn, error, off")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
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
	root.AddCommand(&cobra.Command{Use: "version", Short: "Print the dcb version", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { a.result["version"] = a.version; return nil }})
	wrapActions(root, a)
	return root
}

func (a *application) logf(format string, args ...any) {
	if a.logLevel == "warn" || a.logLevel == "error" || a.logLevel == "off" {
		return
	}
	_, _ = fmt.Fprintf(a.logs, format+"\n", args...)
}

// Cobra validates required flags after pre-run hooks. Mark operation errors only
// once the action begins so parser/argument/required-flag errors remain exit 2.
func wrapActions(cmd *cobra.Command, a *application) {
	if cmd.RunE != nil {
		action := cmd.RunE
		cmd.RunE = func(cmd *cobra.Command, args []string) error { a.running = true; return action(cmd, args) }
	}
	for _, child := range cmd.Commands() {
		wrapActions(child, a)
	}
}
